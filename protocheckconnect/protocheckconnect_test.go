package protocheckconnect_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"connectrpc.com/connect"
	"github.com/trendvidia/protocheck/protocheckconnect"
	"github.com/trendvidia/protocheck/v2"
	schemav1 "github.com/trendvidia/protos/protowire/schema/v1"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

// The tests send wrapperspb.StringValue through real Connect handlers, so they
// need no generated service. ruleFile gives that type a rule without changing
// it: a listed file's rules replace those of a linked message of the same name.
// The rule refuses the value "bad".
func ruleFile(t *testing.T) protoreflect.FileDescriptor {
	t.Helper()
	opts := &descriptorpb.FieldOptions{}
	proto.SetExtension(opts, schemav1.E_FieldAnnotations, &schemav1.AnnotationList{
		Entries: []*schemav1.Annotation{{
			Name: "protowire.schema.v1.validate",
			Args: []*schemav1.AnnotationArg{
				{Value: &schemav1.AnnotationArg_Expression{Expression: &schemav1.Expression{Source: `this != "bad"`}}},
				{Name: "code", Value: &schemav1.AnnotationArg_StringValue{StringValue: "word.bad"}},
			},
		}},
	})
	fd, err := protodesc.NewFile(&descriptorpb.FileDescriptorProto{
		Name:    proto.String("rule.proto"),
		Package: proto.String("google.protobuf"),
		Syntax:  proto.String("proto3"),
		MessageType: []*descriptorpb.DescriptorProto{{
			Name: proto.String("StringValue"),
			Field: []*descriptorpb.FieldDescriptorProto{{
				Name:    proto.String("value"),
				Number:  proto.Int32(1),
				Type:    descriptorpb.FieldDescriptorProto_TYPE_STRING.Enum(),
				Label:   descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL.Enum(),
				Options: opts,
			}},
		}},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	return fd
}

func interceptor(t *testing.T, opts ...protocheckconnect.Option) *protocheckconnect.Interceptor {
	t.Helper()
	v, err := protocheck.New(protocheck.WithDescriptors(ruleFile(t)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { v.Close() })
	icpt, err := protocheckconnect.NewInterceptor(v, opts...)
	if err != nil {
		t.Fatal(err)
	}
	return icpt
}

type str = wrapperspb.StringValue

// handlers is the service under test. An echo answers with reply when it is
// set and with the request otherwise; calls counts the requests that reached
// application code.
type handlers struct {
	reply string
	calls int
	// bidi receives until an error. ignoreErrors makes it drop the error and
	// return nil, as a careless handler would.
	ignoreErrors bool
}

func (h *handlers) answer(in string) *str {
	if h.reply != "" {
		return wrapperspb.String(h.reply)
	}
	return wrapperspb.String(in)
}

func (h *handlers) mux(opts ...connect.HandlerOption) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/t.S/Unary", connect.NewUnaryHandler("/t.S/Unary",
		func(_ context.Context, req *connect.Request[str]) (*connect.Response[str], error) {
			h.calls++
			return connect.NewResponse(h.answer(req.Msg.GetValue())), nil
		}, opts...))
	mux.Handle("/t.S/Client", connect.NewClientStreamHandler("/t.S/Client",
		func(_ context.Context, stream *connect.ClientStream[str]) (*connect.Response[str], error) {
			for stream.Receive() {
				h.calls++
			}
			if err := stream.Err(); err != nil {
				if h.ignoreErrors {
					return connect.NewResponse(wrapperspb.String("swallowed")), nil
				}
				return nil, err
			}
			return connect.NewResponse(h.answer("done")), nil
		}, opts...))
	mux.Handle("/t.S/Server", connect.NewServerStreamHandler("/t.S/Server",
		func(_ context.Context, req *connect.Request[str], stream *connect.ServerStream[str]) error {
			h.calls++
			return stream.Send(h.answer(req.Msg.GetValue()))
		}, opts...))
	mux.Handle("/t.S/Bidi", connect.NewBidiStreamHandler("/t.S/Bidi",
		func(_ context.Context, stream *connect.BidiStream[str, str]) error {
			for {
				msg, err := stream.Receive()
				if errors.Is(err, io.EOF) {
					return nil
				}
				if err != nil {
					if h.ignoreErrors {
						return nil
					}
					return err
				}
				h.calls++
				if err := stream.Send(h.answer(msg.GetValue())); err != nil {
					return err
				}
			}
		}, opts...))
	return mux
}

// server runs h over HTTP/2, which bidirectional streams need. A nil icpt
// leaves that side unvalidated.
func server(t *testing.T, h *handlers, icpt *protocheckconnect.Interceptor) *httptest.Server {
	t.Helper()
	var opts []connect.HandlerOption
	if icpt != nil {
		opts = append(opts, connect.WithInterceptors(icpt))
	}
	srv := httptest.NewUnstartedServer(h.mux(opts...))
	srv.EnableHTTP2 = true
	srv.StartTLS()
	t.Cleanup(srv.Close)
	return srv
}

func client(srv *httptest.Server, method string, icpt *protocheckconnect.Interceptor) *connect.Client[str, str] {
	var opts []connect.ClientOption
	if icpt != nil {
		opts = append(opts, connect.WithInterceptors(icpt))
	}
	return connect.NewClient[str, str](srv.Client(), srv.URL+"/t.S/"+method, opts...)
}

func wantFailure(t *testing.T, err error, code connect.Code) {
	t.Helper()
	if connect.CodeOf(err) != code {
		t.Fatalf("err = %v, want code %v", err, code)
	}
	report := protocheckconnect.ReportOf(err)
	if report == nil || len(report.GetViolations()) != 1 {
		t.Fatalf("report = %v, want one violation", report)
	}
	if got := report.GetViolations()[0].GetCause().GetCode(); got != "word.bad" {
		t.Errorf("violation code = %q, want word.bad", got)
	}
	if got := report.GetMessageType(); got != "google.protobuf.StringValue" {
		t.Errorf("report message type = %q", got)
	}
}

func call(t *testing.T, c *connect.Client[str, str], value string) (*connect.Response[str], error) {
	t.Helper()
	return c.CallUnary(t.Context(), connect.NewRequest(wrapperspb.String(value)))
}

func TestUnaryValidMessagePasses(t *testing.T) {
	h := &handlers{}
	srv := server(t, h, interceptor(t))
	res, err := call(t, client(srv, "Unary", interceptor(t)), "fine")
	if err != nil {
		t.Fatal(err)
	}
	if res.Msg.GetValue() != "fine" || h.calls != 1 {
		t.Fatalf("response %q after %d handler calls", res.Msg.GetValue(), h.calls)
	}
}

func TestUnaryServerRefusesBadRequest(t *testing.T) {
	h := &handlers{}
	srv := server(t, h, interceptor(t))
	_, err := call(t, client(srv, "Unary", nil), "bad")
	wantFailure(t, err, connect.CodeInvalidArgument)
	if h.calls != 0 {
		t.Errorf("handler ran %d times on a bad request", h.calls)
	}
}

// A client is a receiver too: a bad response fails locally and is INTERNAL,
// since the caller did nothing wrong.
func TestUnaryClientRefusesBadResponse(t *testing.T) {
	srv := server(t, &handlers{reply: "bad"}, nil)
	_, err := call(t, client(srv, "Unary", interceptor(t)), "fine")
	wantFailure(t, err, connect.CodeInternal)
	var ve *protocheck.ValidationError
	if !errors.As(err, &ve) {
		t.Errorf("err = %T, want it to wrap *ValidationError", err)
	}
}

func TestSenderValidation(t *testing.T) {
	t.Run("off by default", func(t *testing.T) {
		srv := server(t, &handlers{reply: "bad"}, interceptor(t))
		if _, err := call(t, client(srv, "Unary", nil), "fine"); err != nil {
			t.Fatalf("a server does not check its responses by default: %v", err)
		}
	})
	t.Run("client stops a bad request", func(t *testing.T) {
		h := &handlers{}
		srv := server(t, h, nil)
		_, err := call(t, client(srv, "Unary", interceptor(t, protocheckconnect.WithSenderValidation())), "bad")
		wantFailure(t, err, connect.CodeInvalidArgument)
		if h.calls != 0 {
			t.Errorf("a refused request reached the server %d times", h.calls)
		}
	})
	t.Run("server stops a bad response", func(t *testing.T) {
		srv := server(t, &handlers{reply: "bad"}, interceptor(t, protocheckconnect.WithSenderValidation()))
		_, err := call(t, client(srv, "Unary", nil), "fine")
		wantFailure(t, err, connect.CodeInternal)
	})
	t.Run("server stops a bad stream message", func(t *testing.T) {
		srv := server(t, &handlers{reply: "bad"}, interceptor(t, protocheckconnect.WithSenderValidation()))
		stream, err := client(srv, "Server", nil).CallServerStream(t.Context(), connect.NewRequest(wrapperspb.String("fine")))
		if err != nil {
			t.Fatal(err)
		}
		defer stream.Close()
		if stream.Receive() {
			t.Fatalf("received %v", stream.Msg())
		}
		wantFailure(t, stream.Err(), connect.CodeInternal)
	})
}

func TestClientStreamEndsAtBadMessage(t *testing.T) {
	for _, ignore := range []bool{false, true} {
		h := &handlers{ignoreErrors: ignore}
		srv := server(t, h, interceptor(t))
		stream := client(srv, "Client", nil).CallClientStream(t.Context())
		// Send reports only local failures, so the outcome is read below.
		for _, value := range []string{"one", "bad", "three"} {
			_ = stream.Send(wrapperspb.String(value))
		}
		_, err := stream.CloseAndReceive()
		wantFailure(t, err, connect.CodeInvalidArgument)
		if h.calls != 1 {
			t.Errorf("ignoreErrors=%v: handler got %d messages, want the 1 before the bad one", ignore, h.calls)
		}
	}
}

func TestServerStreamRefusesBadRequest(t *testing.T) {
	h := &handlers{}
	srv := server(t, h, interceptor(t))
	stream, err := client(srv, "Server", nil).CallServerStream(t.Context(), connect.NewRequest(wrapperspb.String("bad")))
	if err == nil {
		defer stream.Close()
		if stream.Receive() {
			t.Fatalf("received %v", stream.Msg())
		}
		err = stream.Err()
	}
	wantFailure(t, err, connect.CodeInvalidArgument)
	if h.calls != 0 {
		t.Errorf("handler ran %d times on a bad request", h.calls)
	}
}

func TestBidiStreamEndsAtBadMessage(t *testing.T) {
	for _, ignore := range []bool{false, true} {
		h := &handlers{ignoreErrors: ignore}
		srv := server(t, h, interceptor(t))
		stream := client(srv, "Bidi", nil).CallBidiStream(t.Context())
		if err := stream.Send(wrapperspb.String("one")); err != nil {
			t.Fatal(err)
		}
		// Messages before the bad one stay delivered (RFC-001 §6.6 rule 4).
		first, err := stream.Receive()
		if err != nil || first.GetValue() != "one" {
			t.Fatalf("first reply = %v, %v", first, err)
		}
		_ = stream.Send(wrapperspb.String("bad"))
		_, err = stream.Receive()
		wantFailure(t, err, connect.CodeInvalidArgument)
		_ = stream.CloseRequest()
		_ = stream.CloseResponse()
	}
}

// After a bad response the client stream stays failed: a later Send or Receive
// cannot carry on as though nothing happened.
func TestClientReceiveFailureIsFinal(t *testing.T) {
	srv := server(t, &handlers{reply: "bad"}, nil)
	stream := client(srv, "Bidi", interceptor(t)).CallBidiStream(t.Context())
	defer stream.CloseResponse()
	defer stream.CloseRequest()
	if err := stream.Send(wrapperspb.String("fine")); err != nil {
		t.Fatal(err)
	}
	_, err := stream.Receive()
	wantFailure(t, err, connect.CodeInternal)
	wantFailure(t, stream.Send(wrapperspb.String("fine")), connect.CodeInternal)
	_, err = stream.Receive()
	wantFailure(t, err, connect.CodeInternal)
}

func TestErrorFunc(t *testing.T) {
	custom := func(code connect.Code, report *protocheck.Report) *connect.Error {
		return connect.NewError(code, errors.New("mine: "+report.Violations[0].Cause.Code))
	}
	srv := server(t, &handlers{}, interceptor(t, protocheckconnect.WithErrorFunc(custom)))
	_, err := call(t, client(srv, "Unary", nil), "bad")
	var cerr *connect.Error
	if !errors.As(err, &cerr) || cerr.Code() != connect.CodeInvalidArgument || cerr.Message() != "mine: word.bad" {
		t.Fatalf("err = %v", err)
	}
	if report := protocheckconnect.ReportOf(err); report != nil {
		t.Errorf("custom error still carries a report: %v", report)
	}

	// A mapping that returns nil must not let the message through.
	none := func(connect.Code, *protocheck.Report) *connect.Error { return nil }
	h := &handlers{}
	srv = server(t, h, interceptor(t, protocheckconnect.WithErrorFunc(none)))
	_, err = call(t, client(srv, "Unary", nil), "bad")
	wantFailure(t, err, connect.CodeInvalidArgument)
	if h.calls != 0 {
		t.Errorf("handler ran %d times", h.calls)
	}
}

type notProto struct{}

// A message that could not be validated is not "invalid": it is INTERNAL, has
// no report, and never reaches the next step.
func TestNotValidatedIsInternal(t *testing.T) {
	icpt := interceptor(t)
	wrapped := icpt.WrapUnary(func(context.Context, connect.AnyRequest) (connect.AnyResponse, error) {
		t.Error("next ran on a message that was not validated")
		return nil, nil
	})
	for name, req := range map[string]connect.AnyRequest{
		"not a proto.Message": connect.NewRequest(&notProto{}),
		"typed nil":           connect.NewRequest[str](nil),
	} {
		_, err := wrapped(t.Context(), req)
		if connect.CodeOf(err) != connect.CodeInternal || protocheckconnect.ReportOf(err) != nil {
			t.Errorf("%s: err = %v, report = %v", name, err, protocheckconnect.ReportOf(err))
		}
	}
}

func TestNilValidator(t *testing.T) {
	if _, err := protocheckconnect.NewInterceptor(nil); err == nil {
		t.Fatal("NewInterceptor(nil) succeeded")
	}
}
