package server

import (
	"context"
	"encoding/json"
	"fmt"
)

// WebViewBridge abstracts the minimal WebView2 host methods used to register native Go RPCs and push events.
type WebViewBridge interface {
	Bind(name string, f interface{}) error
	Dispatch(f func())
	Eval(js string)
}

// BindWebView attaches a non-blocking async RPC dispatcher ("__appCall") and real-time event sink
// so business operations execute over WebView2 IPC without exposing HTTP APIs or SSE streams.
func (s *Server) BindWebView(wv WebViewBridge) error {
	if wv == nil {
		return nil
	}

	s.SetEventSink(func(event string, data interface{}) {
		eventLit, _ := json.Marshal(event)
		var callJS string
		if data == nil {
			callJS = fmt.Sprintf("if (window.__onBackendEvent) window.__onBackendEvent(%s);", eventLit)
		} else {
			dataLit, err := json.Marshal(data)
			if err != nil {
				return
			}
			callJS = fmt.Sprintf("if (window.__onBackendEvent) window.__onBackendEvent(%s, %s);", eventLit, dataLit)
		}
		wv.Dispatch(func() {
			wv.Eval(callJS)
		})
	})

	return wv.Bind("__appCall", func(id int, method string, paramsJSON string) {
		reject := func(err error) {
			errLit, _ := json.Marshal(err.Error())
			wv.Dispatch(func() {
				wv.Eval(fmt.Sprintf("if (window.__rpcReject) window.__rpcReject(%d, %s);", id, errLit))
			})
		}

		go func() {
			defer func() {
				if r := recover(); r != nil {
					reject(fmt.Errorf("内部异常 (%s): %v", method, r))
				}
			}()
			res, err := s.HandleRPC(context.Background(), method, json.RawMessage(paramsJSON))
			if err != nil {
				reject(err)
				return
			}
			resLit, err := json.Marshal(res)
			if err != nil {
				reject(err)
				return
			}
			wv.Dispatch(func() {
				wv.Eval(fmt.Sprintf("if (window.__rpcResolve) window.__rpcResolve(%d, %s);", id, resLit))
			})
		}()
	})
}
