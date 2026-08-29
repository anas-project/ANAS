package consolelistener

import (
	"context"
	"errors"
	"net"
	"reflect"
	"syscall"
	"testing"

	"github.com/anas-project/ANAS/internal/consoleconfig"
)

func TestSpecsAreStaticWildcardOrLoopbackPairs(t *testing.T) {
	for _, test := range []struct {
		mode consoleconfig.Mode
		want []Spec
	}{
		{
			mode: consoleconfig.ModeLAN,
			want: []Spec{{Network: "tcp4", Address: "0.0.0.0:8443"}, {Network: "tcp6", Address: "[::]:8443", IPv6Optional: true}},
		},
		{
			mode: consoleconfig.ModeLoopback,
			want: []Spec{{Network: "tcp4", Address: "127.0.0.1:8443"}, {Network: "tcp6", Address: "[::1]:8443", IPv6Optional: true}},
		},
	} {
		got, err := Specs(test.mode, 8443)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, test.want) {
			t.Fatalf("Specs(%q) = %#v, want %#v", test.mode, got, test.want)
		}
	}
}

func TestListenAllSkipsOnlyUnavailableIPv6(t *testing.T) {
	var calls []string
	listeners, err := ListenAll(context.Background(), []Spec{
		{Network: "tcp4", Address: "0.0.0.0:8080"},
		{Network: "tcp6", Address: "[::]:8080", IPv6Optional: true},
	}, func(_ context.Context, network, address string) (net.Listener, error) {
		calls = append(calls, network+" "+address)
		if network == "tcp6" {
			return nil, syscall.EAFNOSUPPORT
		}
		return &stubListener{address: stubAddress(address)}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(listeners) != 1 || !reflect.DeepEqual(calls, []string{"tcp4 0.0.0.0:8080", "tcp6 [::]:8080"}) {
		t.Fatalf("listeners = %d, calls = %#v", len(listeners), calls)
	}
}

func TestListenAllFailsClosedAndClosesEarlierListener(t *testing.T) {
	first := &stubListener{address: "0.0.0.0:8080"}
	_, err := ListenAll(context.Background(), []Spec{
		{Network: "tcp4", Address: "0.0.0.0:8080"},
		{Network: "tcp6", Address: "[::]:8080", IPv6Optional: true},
	}, func(_ context.Context, network, _ string) (net.Listener, error) {
		if network == "tcp4" {
			return first, nil
		}
		return nil, errors.New("permission denied")
	})
	if err == nil || !first.closed {
		t.Fatalf("error = %v, first closed = %v", err, first.closed)
	}
}

type stubListener struct {
	address stubAddress
	closed  bool
}

func (*stubListener) Accept() (net.Conn, error) { return nil, net.ErrClosed }
func (listener *stubListener) Close() error     { listener.closed = true; return nil }
func (listener *stubListener) Addr() net.Addr   { return listener.address }

type stubAddress string

func (stubAddress) Network() string { return "tcp" }
func (address stubAddress) String() string {
	return string(address)
}
