// Copyright 2026 TrendVidia LLC
// SPDX-License-Identifier: MIT

// Package protocheckconnect validates ConnectRPC messages with protocheck,
// following the RPC contract of RFC-001 §6.6.
//
// It is a nested Go module so protocheck core picks up no Connect dependency;
// consumers opt in with:
//
//	go get github.com/trendvidia/protocheck/protocheckconnect
//
//	v, err := protocheck.New()
//	...
//	icpt, err := protocheckconnect.NewInterceptor(v)
//	...
//	path, handler := acmev1connect.NewSongServiceHandler(svc, connect.WithInterceptors(icpt))
//
// The same interceptor works on a client. Whichever side receives a message
// validates it before application code sees it, so a handler never runs on a
// bad request and a client never gets a bad response:
//
//   - An invalid request is refused with CodeInvalidArgument.
//   - An invalid response fails with CodeInternal. On a client this is a local
//     error: the server was not told, and the caller is not to blame.
//   - An invalid message on a stream ends the stream. Every later Send and
//     Receive returns the same error, and a stream handler that ignores the
//     error still ends with it. RFC-001 rules out a lenient mode; to handle
//     failures yourself, leave the interceptor off and call Validate.
//
// Each of these errors wraps a *protocheck.ValidationError and carries the
// protowire.schema.v1.Report as a detail, which ReportOf reads back. The
// report tells the peer about the schema: rule codes, messages, source
// locations, and the offending values, except those of @sensitive fields.
// WithErrorFunc replaces it with a detail of your own.
//
// The error's message uses each violation's fallback message. WithLocale
// renders it in the caller's locale instead, through the catalogs registered
// with the Validator (protocheck.WithCatalog). The report detail is the same
// either way: a client can localize it from codes and params itself.
//
// An error that is not about the message, such as a rule that cannot be
// loaded, is CodeInternal with no report: the message was not validated.
package protocheckconnect

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"

	"connectrpc.com/connect"
	"github.com/trendvidia/protocheck/v2"
	schemav1 "github.com/trendvidia/protos/protowire/schema/v1"
	"google.golang.org/protobuf/proto"
)

// Option configures an Interceptor.
type Option func(*Interceptor)

// WithSenderValidation also validates each message before it is sent: requests
// on a client and responses on a server. A receiver validates either way, so
// this finds a bad message sooner, at the cost of validating it twice.
func WithSenderValidation() Option {
	return func(i *Interceptor) { i.sender = true }
}

// WithErrorFunc replaces the error built from a failed validation, for an API
// with its own error detail. code is CodeInvalidArgument for a request and
// CodeInternal for a response. If fn returns nil, the default error is used,
// so a mapping can never let an invalid message through.
func WithErrorFunc(fn func(code connect.Code, report *protocheck.Report) *connect.Error) Option {
	return func(i *Interceptor) { i.errorFunc = fn }
}

// WithLocale renders a failed validation's error message in a locale that fn
// picks from the request header, such as from Accept-Language. Each message
// comes from the catalog registered for that locale with
// protocheck.WithCatalog or Engine.RegisterCatalog. A violation the catalog
// does not cover, or a locale with no catalog, keeps its fallback message.
// Choosing the locale is the server's policy (RFC-001 §7), so fn decides how
// to match the header against the registered locales.
//
// On a client, fn gets the header the client sends. A stream reads the locale
// once: a handler stream before the handler runs, a client stream at its first
// Send.
func WithLocale(fn func(header http.Header) string) Option {
	return func(i *Interceptor) { i.locale = fn }
}

// Interceptor is a connect.Interceptor that validates messages with a
// protocheck.Validator. It is safe for concurrent use.
type Interceptor struct {
	engine    protocheck.Engine
	sender    bool
	errorFunc func(connect.Code, *protocheck.Report) *connect.Error
	locale    func(http.Header) string
}

var _ connect.Interceptor = (*Interceptor)(nil)

// NewInterceptor builds an Interceptor around v.
func NewInterceptor(v *protocheck.Validator, opts ...Option) (*Interceptor, error) {
	if v == nil {
		return nil, errors.New("protocheckconnect: nil Validator")
	}
	i := &Interceptor{engine: v.Engine()}
	for _, opt := range opts {
		opt(i)
	}
	return i, nil
}

// ReportOf returns the validation report carried by err, or nil if it has none.
func ReportOf(err error) *schemav1.Report {
	var cerr *connect.Error
	if !errors.As(err, &cerr) {
		return nil
	}
	for _, detail := range cerr.Details() {
		if value, verr := detail.Value(); verr == nil {
			if report, ok := value.(*schemav1.Report); ok {
				return report
			}
		}
	}
	return nil
}

// WrapUnary implements connect.Interceptor.
func (i *Interceptor) WrapUnary(next connect.UnaryFunc) connect.UnaryFunc {
	return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
		isClient := req.Spec().IsClient
		locale := i.localeOf(req.Header())
		if !isClient || i.sender {
			if err := i.check(req.Any(), connect.CodeInvalidArgument, locale); err != nil {
				return nil, err
			}
		}
		res, err := next(ctx, req)
		if err != nil {
			return nil, err
		}
		if isClient || i.sender {
			if err := i.check(res.Any(), connect.CodeInternal, locale); err != nil {
				return nil, err
			}
		}
		return res, nil
	}
}

// WrapStreamingClient implements connect.Interceptor.
func (i *Interceptor) WrapStreamingClient(next connect.StreamingClientFunc) connect.StreamingClientFunc {
	return func(ctx context.Context, spec connect.Spec) connect.StreamingClientConn {
		return &clientConn{StreamingClientConn: next(ctx, spec), interceptor: i}
	}
}

// WrapStreamingHandler implements connect.Interceptor.
func (i *Interceptor) WrapStreamingHandler(next connect.StreamingHandlerFunc) connect.StreamingHandlerFunc {
	return func(ctx context.Context, conn connect.StreamingHandlerConn) error {
		wrapped := &handlerConn{StreamingHandlerConn: conn, interceptor: i, locale: i.localeOf(conn.RequestHeader())}
		err := next(ctx, wrapped)
		if failed := wrapped.err(); failed != nil {
			return failed
		}
		return err
	}
}

// localeOf returns the locale WithLocale picks from header, "" without it.
func (i *Interceptor) localeOf(header http.Header) string {
	if i.locale == nil {
		return ""
	}
	return i.locale(header)
}

// check validates msg and returns the error to fail with, nil if it is valid.
// msg arrives as any, so one that is not a proto.Message cannot be checked and
// is refused. locale is the one WithLocale picked for the call.
func (i *Interceptor) check(msg any, code connect.Code, locale string) error {
	m, ok := msg.(proto.Message)
	if !ok {
		return connect.NewError(connect.CodeInternal, fmt.Errorf("protocheckconnect: %T is not a proto.Message", msg))
	}
	report, err := i.engine.Validate(m)
	if err != nil {
		return connect.NewError(connect.CodeInternal, err)
	}
	if len(report.Violations) == 0 {
		return nil
	}
	if i.errorFunc != nil {
		if cerr := i.errorFunc(code, report); cerr != nil {
			return cerr
		}
	}
	var failure error = &protocheck.ValidationError{Violations: report.Violations}
	if i.locale != nil {
		failure = localized(report, locale, failure)
	}
	cerr := connect.NewError(code, failure)
	if detail, derr := connect.NewErrorDetail(report.Proto()); derr == nil {
		cerr.AddDetail(detail)
	}
	return cerr
}

// localizedError is a ValidationError whose message is rendered in a locale.
type localizedError struct {
	msg string
	err error
}

func (e *localizedError) Error() string { return e.msg }
func (e *localizedError) Unwrap() error { return e.err }

// localized renders report in locale, in the shape ValidationError.Error
// gives its fallback messages.
func localized(report *protocheck.Report, locale string, err error) error {
	lines := report.Format(locale)
	var b strings.Builder
	if len(lines) == 1 {
		b.WriteString("validation failed: ")
		b.WriteString(lines[0])
	} else {
		fmt.Fprintf(&b, "validation failed with %d violations:", len(lines))
		for _, line := range lines {
			b.WriteString("\n  - ")
			b.WriteString(line)
		}
	}
	return &localizedError{msg: b.String(), err: err}
}

// failure holds the first error of a stream, which then ends it. Send and
// Receive may run on different goroutines.
type failure struct {
	first atomic.Pointer[error]
}

func (f *failure) fail(err error) error {
	f.first.CompareAndSwap(nil, &err)
	return f.err()
}

func (f *failure) err() error {
	if first := f.first.Load(); first != nil {
		return *first
	}
	return nil
}

type clientConn struct {
	connect.StreamingClientConn
	failure
	interceptor *Interceptor
	// locale is read at the first Send: RequestHeader belongs to the write
	// side of a client stream, so Receive must not read it.
	locale atomic.Pointer[string]
}

func (c *clientConn) localeOf() string {
	if l := c.locale.Load(); l != nil {
		return *l
	}
	return ""
}

func (c *clientConn) Send(msg any) error {
	if c.locale.Load() == nil {
		l := c.interceptor.localeOf(c.RequestHeader())
		c.locale.Store(&l)
	}
	if err := c.err(); err != nil {
		return err
	}
	if c.interceptor.sender {
		if err := c.interceptor.check(msg, connect.CodeInvalidArgument, c.localeOf()); err != nil {
			return c.fail(err)
		}
	}
	return c.StreamingClientConn.Send(msg)
}

func (c *clientConn) Receive(msg any) error {
	if err := c.err(); err != nil {
		return err
	}
	if err := c.StreamingClientConn.Receive(msg); err != nil {
		return err
	}
	if err := c.interceptor.check(msg, connect.CodeInternal, c.localeOf()); err != nil {
		return c.fail(err)
	}
	return nil
}

type handlerConn struct {
	connect.StreamingHandlerConn
	failure
	interceptor *Interceptor
	locale      string // read before the handler runs
}

func (c *handlerConn) Send(msg any) error {
	if err := c.err(); err != nil {
		return err
	}
	if c.interceptor.sender {
		if err := c.interceptor.check(msg, connect.CodeInternal, c.locale); err != nil {
			return c.fail(err)
		}
	}
	return c.StreamingHandlerConn.Send(msg)
}

func (c *handlerConn) Receive(msg any) error {
	if err := c.err(); err != nil {
		return err
	}
	if err := c.StreamingHandlerConn.Receive(msg); err != nil {
		return err
	}
	if err := c.interceptor.check(msg, connect.CodeInvalidArgument, c.locale); err != nil {
		return c.fail(err)
	}
	return nil
}
