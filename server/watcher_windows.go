package server

import (
	"os"
	"sync"
	"sync/atomic"

	"golang.org/x/sys/windows"
)

const fileNotifyMask = windows.FILE_NOTIFY_CHANGE_LAST_WRITE |
	windows.FILE_NOTIFY_CHANGE_SIZE |
	windows.FILE_NOTIFY_CHANGE_FILE_NAME

type fileWatcher struct {
	wake       windows.Handle
	stop       windows.Handle
	done       chan struct{}
	closeOnce  sync.Once
	selfWrites atomic.Int32
}

func (s *Server) initWatcher() {
	if wake, err := windows.CreateEvent(nil, 0, 0, nil); err == nil {
		s.watcher.wake = wake
	}
	if stop, err := windows.CreateEvent(nil, 1, 0, nil); err == nil {
		s.watcher.stop = stop
	}
	s.watcher.done = make(chan struct{})
}

func (s *Server) notifyWatcher() {
	if s.watcher.wake != 0 {
		_ = windows.SetEvent(s.watcher.wake)
	}
}

func (s *Server) beginSelfWrite() {
	s.watcher.selfWrites.Add(1)
}

func (s *Server) endSelfWrite() {
	s.watcher.selfWrites.Add(-1)
}

// Close stops the directory change watcher and closes the HTTP listener.
func (s *Server) Close() error {
	var closeErr error
	s.watcher.closeOnce.Do(func() {
		if s.watcher.stop != 0 {
			_ = windows.SetEvent(s.watcher.stop)
		}
		if s.watcher.done != nil {
			<-s.watcher.done
		}
		if s.watcher.wake != 0 {
			_ = windows.CloseHandle(s.watcher.wake)
			s.watcher.wake = 0
		}
		if s.watcher.stop != 0 {
			_ = windows.CloseHandle(s.watcher.stop)
			s.watcher.stop = 0
		}
		if s.listener != nil {
			closeErr = s.listener.Close()
		}
	})
	return closeErr
}

func (s *Server) activeWatchDir() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.isMemoryOnly || s.currentFilePath == "" {
		return ""
	}
	return s.currentDir
}

func (s *Server) checkActiveFileChanged() {
	if s.watcher.selfWrites.Load() > 0 {
		return
	}

	s.mu.RLock()
	isMem := s.isMemoryOnly
	filePath := s.currentFilePath
	lastMod := s.lastModTime
	lastSize := s.fileSize
	seq := s.docSeq
	s.mu.RUnlock()

	if isMem || filePath == "" {
		return
	}

	info, err := os.Stat(filePath)
	if err != nil {
		return
	}

	if info.ModTime().After(lastMod) || info.Size() != lastSize {
		s.mu.Lock()
		changed := s.watcher.selfWrites.Load() == 0 &&
			!s.isMemoryOnly &&
			s.currentFilePath == filePath &&
			s.docSeq == seq &&
			(info.ModTime().After(s.lastModTime) || info.Size() != s.fileSize)
		if changed {
			s.lastModTime = info.ModTime()
			s.fileSize = info.Size()
			s.docSeq++
		}
		s.mu.Unlock()

		if changed {
			s.TriggerEvent("reload")
		}
	}
}

// watchLoop waits on Win32 directory change notifications (zero polling) and emits "reload"
// when the currently open disk file is modified or atomically replaced externally.
func (s *Server) watchLoop() {
	defer close(s.watcher.done)
	if s.watcher.wake == 0 || s.watcher.stop == 0 {
		return
	}

	var watchedDir string
	var dirHandle windows.Handle

	closeDirHandle := func() {
		if dirHandle != 0 && dirHandle != windows.InvalidHandle {
			_ = windows.FindCloseChangeNotification(dirHandle)
		}
		dirHandle = 0
		watchedDir = ""
	}
	defer closeDirHandle()

	for {
		wantDir := s.activeWatchDir()
		if wantDir != watchedDir {
			closeDirHandle()
			if wantDir != "" {
				if h, err := windows.FindFirstChangeNotification(wantDir, false, fileNotifyMask); err == nil && h != 0 && h != windows.InvalidHandle {
					dirHandle = h
					watchedDir = wantDir
					s.checkActiveFileChanged()
				}
			}
		}

		handles := make([]windows.Handle, 0, 3)
		handles = append(handles, s.watcher.wake, s.watcher.stop)
		hasDir := dirHandle != 0 && dirHandle != windows.InvalidHandle
		if hasDir {
			handles = append(handles, dirHandle)
		}

		ev, err := windows.WaitForMultipleObjects(handles, false, windows.INFINITE)
		if err != nil {
			return
		}

		switch ev {
		case windows.WAIT_OBJECT_0:
			// Active directory or document changed; next iteration updates dirHandle.
			continue
		case windows.WAIT_OBJECT_0 + 1:
			// Server shutdown signaled.
			return
		case windows.WAIT_OBJECT_0 + 2:
			if hasDir {
				if err := windows.FindNextChangeNotification(dirHandle); err != nil {
					closeDirHandle()
				}
			}
			s.checkActiveFileChanged()
		default:
			if hasDir {
				closeDirHandle()
			}
		}
	}
}
