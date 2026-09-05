package clientconnection

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"time"
)

// Operation separates body reception from response lifetime. Its context is
// returned to the caller rather than retained inside the operation.
type Operation struct {
	cancel     context.CancelCauseFunc
	finished   chan struct{}
	once       sync.Once
	connection *Connection
	request    *http.Request
	controller *http.ResponseController
}

func Begin(request *http.Request, writer http.ResponseWriter) (context.Context, *Operation) {
	base := request.Context()
	observed := FromContext(base)
	if request.ProtoMajor != 1 {
		observed = nil
	}
	if observed != nil {
		base = context.WithoutCancel(base)
	}
	ctx, cancel := context.WithCancelCause(base)
	operation := &Operation{cancel: cancel, finished: make(chan struct{}), connection: observed, request: request, controller: http.NewResponseController(writer)}
	if observed != nil {
		go func() {
			select {
			case <-observed.Done():
				cancel(context.Canceled)
			case <-operation.finished:
			}
		}()
	}
	return ctx, operation
}

func (o *Operation) Cancel(cause error) { o.cancel(cause) }

// Interrupt only ends reception. HTTP/2 retains its stream cancellation signal;
// HTTP/1 uses physical connection observation after the parser read is stopped.
func (o *Operation) Interrupt(_ error) {
	if o.request.ProtoMajor == 1 {
		o.request.Close = true
		if o.connection != nil {
			o.connection.DiscardInput()
		}
		_ = o.controller.SetReadDeadline(time.Now())
	}
}

func (o *Operation) Close() {
	o.once.Do(func() { close(o.finished); o.cancel(context.Canceled) })
}

func (o *Operation) EnableDuplex() error {
	err := o.controller.EnableFullDuplex()
	if errors.Is(err, http.ErrNotSupported) && o.request.ProtoMajor >= 2 {
		return nil
	}
	return err
}
