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

	s.SetEventSink(func(event string) {
		payload, _ := json.Marshal(event)
		wv.Dispatch(func() {
			wv.Eval(fmt.Sprintf("if (window.__onBackendEvent) window.__onBackendEvent(%s);", payload))
		})
	})

	return wv.Bind("__appCall", func(id int, method string, paramsJSON string) {
		go func() {
			res, err := s.HandleRPC(context.Background(), method, json.RawMessage(paramsJSON))
			if err != nil {
				errLit, _ := json.Marshal(err.Error())
				wv.Dispatch(func() {
					wv.Eval(fmt.Sprintf("if (window.__rpcReject) window.__rpcReject(%d, %s);", id, errLit))
				})
				return
			}
			resLit, err := json.Marshal(res)
			if err != nil {
				errLit, _ := json.Marshal(err.Error())
				wv.Dispatch(func() {
					wv.Eval(fmt.Sprintf("if (window.__rpcReject) window.__rpcReject(%d, %s);", id, errLit))
				})
				return
			}
			wv.Dispatch(func() {
				wv.Eval(fmt.Sprintf("if (window.__rpcResolve) window.__rpcResolve(%d, %s);", id, resLit))
			})
		}()
	})
}
