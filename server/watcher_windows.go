package server

import (
	"os"
	"path/filepath"
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

func closeWatchHandle(h *windows.Handle) {
	if *h != 0 && *h != windows.InvalidHandle {
		_ = windows.FindCloseChangeNotification(*h)
	}
	*h = 0
}

func ancestorWatchTarget(dir string, dirAttached bool) (string, bool) {
	parent := filepath.Dir(dir)
	if parent == "" || parent == dir {
		return "", false
	}
	if dirAttached {
		if info, err := os.Stat(parent); err == nil && info.IsDir() {
			return parent, false
		}
		return "", false
	}
	cur := parent
	subtree := false
	for cur != "" {
		if info, err := os.Stat(cur); err == nil && info.IsDir() {
			return cur, subtree
		}
		next := filepath.Dir(cur)
		if next == cur {
			break
		}
		cur = next
		subtree = true
	}
	return "", false
}

// watchLoop waits on Win32 directory change notifications (zero polling) and emits "reload"
// when the currently open disk file is modified or its directory is externally deleted/renamed and recreated.
func (s *Server) watchLoop() {
	defer close(s.watcher.done)
	if s.watcher.wake == 0 || s.watcher.stop == 0 {
		return
	}

	var watchedDir string
	var watchedInfo os.FileInfo
	var dirHandle windows.Handle
	var ancestorPath string
	var ancestorSubtree bool
	var ancestorHandle windows.Handle

	clearDirWatch := func() {
		closeWatchHandle(&dirHandle)
		watchedInfo = nil
	}
	clearAncestorWatch := func() {
		closeWatchHandle(&ancestorHandle)
		ancestorPath = ""
		ancestorSubtree = false
	}
	defer func() {
		clearDirWatch()
		clearAncestorWatch()
	}()

	for {
		wantDir := s.activeWatchDir()
		if wantDir != watchedDir {
			clearDirWatch()
			clearAncestorWatch()
			watchedDir = wantDir
		}

		if watchedDir != "" {
			curInfo, statErr := os.Stat(watchedDir)
			if dirHandle != 0 && (statErr != nil || !curInfo.IsDir() || (watchedInfo != nil && !os.SameFile(watchedInfo, curInfo))) {
				clearDirWatch()
			}
			if dirHandle == 0 && statErr == nil && curInfo.IsDir() {
				if h, err := windows.FindFirstChangeNotification(watchedDir, false, fileNotifyMask); err == nil && h != 0 && h != windows.InvalidHandle {
					dirHandle = h
					watchedInfo = curInfo
					s.checkActiveFileChanged()
				}
			}

			wantAnc, wantSubtree := ancestorWatchTarget(watchedDir, dirHandle != 0)
			if wantAnc != ancestorPath || wantSubtree != ancestorSubtree {
				clearAncestorWatch()
				if wantAnc != "" {
					if h, err := windows.FindFirstChangeNotification(wantAnc, wantSubtree, windows.FILE_NOTIFY_CHANGE_DIR_NAME); err == nil && h != 0 && h != windows.InvalidHandle {
						ancestorHandle = h
						ancestorPath = wantAnc
						ancestorSubtree = wantSubtree
						if dirHandle == 0 {
							if reInfo, reErr := os.Stat(watchedDir); reErr == nil && reInfo.IsDir() {
								continue
							}
						}
					}
				}
			}
		}

		handles := make([]windows.Handle, 0, 4)
		handles = append(handles, s.watcher.wake, s.watcher.stop)
		dirIdx, ancIdx := -1, -1
		if dirHandle != 0 && dirHandle != windows.InvalidHandle {
			dirIdx = len(handles)
			handles = append(handles, dirHandle)
		}
		if ancestorHandle != 0 && ancestorHandle != windows.InvalidHandle {
			ancIdx = len(handles)
			handles = append(handles, ancestorHandle)
		}

		ev, err := windows.WaitForMultipleObjects(handles, false, windows.INFINITE)
		if err != nil {
			return
		}

		switch idx := int(ev - windows.WAIT_OBJECT_0); {
		case idx == 0:
			// Active directory or document changed; next iteration updates watches.
			continue
		case idx == 1:
			// Server shutdown signaled.
			return
		case idx == dirIdx:
			if err := windows.FindNextChangeNotification(dirHandle); err != nil {
				clearDirWatch()
			}
			s.checkActiveFileChanged()
		case idx == ancIdx:
			if err := windows.FindNextChangeNotification(ancestorHandle); err != nil {
				clearAncestorWatch()
			}
		default:
			clearDirWatch()
			clearAncestorWatch()
		}
	}
}
