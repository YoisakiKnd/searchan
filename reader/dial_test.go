package reader

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"sync/atomic"
	"testing"
	"time"
)

type observedConn struct {
	net.Conn
	closed chan struct{}
	closes atomic.Int32
}

func newObservedConn() *observedConn { return &observedConn{closed: make(chan struct{})} }
func (c *observedConn) Close() error {
	if c.closes.Add(1) == 1 {
		close(c.closed)
	}
	return nil
}

func waitDialSignal(t *testing.T, ch <-chan struct{}) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(time.Second):
		t.Fatal("connection attempt or cleanup did not finish")
	}
}

func TestPublicDialFallsBackFromStalledAddressFamily(t *testing.T) {
	for _, ips := range [][]netip.Addr{
		{netip.MustParseAddr("2606:4700:4700::1111"), netip.MustParseAddr("1.1.1.1")},
		{netip.MustParseAddr("1.1.1.1"), netip.MustParseAddr("2606:4700:4700::1111")},
	} {
		t.Run(ips[0].String(), func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			winner := newObservedConn()
			defer winner.Close()
			primaryStopped := make(chan struct{})
			var calls atomic.Int32
			conn, err := dialPublicIPs(ctx, "tcp", "443", ips, func(ctx context.Context, network, address string) (net.Conn, error) {
				calls.Add(1)
				if address == net.JoinHostPort(ips[0].String(), "443") {
					<-ctx.Done()
					close(primaryStopped)
					return nil, ctx.Err()
				}
				return winner, nil
			}, 5*time.Millisecond)
			if err != nil || conn != winner || calls.Load() != 2 || winner.closes.Load() != 0 {
				t.Fatal("stalled first family prevented usable connection", err)
			}
			waitDialSignal(t, primaryStopped)
		})
	}
}

func TestPublicDialDoesNotDelayEarlyFailureOrRaceSingleFamily(t *testing.T) {
	for _, secondary := range []string{"2606:4700:4700::1111", "8.8.8.8"} {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		ips := []netip.Addr{netip.MustParseAddr("1.1.1.1"), netip.MustParseAddr(secondary)}
		winner := newObservedConn()
		var calls atomic.Int32
		conn, err := dialPublicIPs(ctx, "tcp", "443", ips, func(context.Context, string, string) (net.Conn, error) {
			if calls.Add(1) == 1 {
				return nil, errors.New("route unavailable")
			}
			return winner, nil
		}, time.Hour)
		cancel()
		if err != nil || conn != winner || calls.Load() != 2 {
			t.Fatal("early failure waited for fallback timer", err)
		}
		_ = conn.Close()
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	winner := newObservedConn()
	var calls atomic.Int32
	conn, err := dialPublicIPs(ctx, "tcp", "443", []netip.Addr{netip.MustParseAddr("1.1.1.1"), netip.MustParseAddr("2606:4700:4700::1111")}, func(context.Context, string, string) (net.Conn, error) {
		calls.Add(1)
		return winner, nil
	}, time.Hour)
	if err != nil || calls.Load() != 1 {
		t.Fatal("fast first address started unnecessary fallback", err)
	}
	_ = conn.Close()
}

func TestPublicDialValidatesAllAddressesBeforeAnyConnection(t *testing.T) {
	for _, raw := range []string{"127.0.0.1", "10.0.0.1", "::ffff:169.254.169.254", "2001:db8::1", "2606:4700:4700::1111%eth0"} {
		ips := []netip.Addr{netip.MustParseAddr("1.1.1.1"), netip.MustParseAddr(raw)}
		_, err := dialPublicIPs(context.Background(), "tcp4", "443", ips, func(context.Context, string, string) (net.Conn, error) {
			t.Fatal("contacted address before rejecting mixed unsafe DNS answer", raw)
			return nil, nil
		}, 0)
		if err == nil {
			t.Fatal("accepted unsafe DNS answer", raw)
		}
	}
	for _, network := range []string{"tcp", "udp"} {
		if _, err := dialPublicIPs(context.Background(), network, "443", nil, nil, 0); err == nil {
			t.Fatal("accepted empty addresses or unsupported network")
		}
	}
}

func TestPublicDialHonorsExplicitFamilyAndUnmapsAddresses(t *testing.T) {
	for _, network := range []string{"tcp4", "tcp6"} {
		winner := newObservedConn()
		ips := []netip.Addr{netip.MustParseAddr("::ffff:1.1.1.1"), netip.MustParseAddr("2606:4700:4700::1111")}
		conn, err := dialPublicIPs(context.Background(), network, "443", ips, func(_ context.Context, gotNetwork, address string) (net.Conn, error) {
			want := "1.1.1.1:443"
			if network == "tcp6" {
				want = "[2606:4700:4700::1111]:443"
			}
			if gotNetwork != network || address != want {
				t.Fatal("connected wrong address family", gotNetwork, address)
			}
			return winner, nil
		}, 0)
		if err != nil {
			t.Fatal(err)
		}
		_ = conn.Close()
	}
}

func TestPublicDialCancellationStopsBothFamilies(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var started, stopped atomic.Int32
	allStopped := make(chan struct{})
	ips := []netip.Addr{netip.MustParseAddr("1.1.1.1"), netip.MustParseAddr("8.8.8.8"), netip.MustParseAddr("2606:4700:4700::1111"), netip.MustParseAddr("2606:4700:4700::1001")}
	_, err := dialPublicIPs(ctx, "tcp", "443", ips, func(ctx context.Context, _, _ string) (net.Conn, error) {
		if started.Add(1) == 2 {
			cancel()
		}
		<-ctx.Done()
		if stopped.Add(1) == 2 {
			close(allStopped)
		}
		return nil, ctx.Err()
	}, 0)
	if !errors.Is(err, context.Canceled) {
		t.Fatal("cancellation cause lost", err)
	}
	waitDialSignal(t, allStopped)
	if started.Load() != 2 {
		t.Fatal("more than two address attempts after cancellation", started.Load())
	}
}

func TestPublicDialClosesLateSuccessfulLoser(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	winner, loser := newObservedConn(), newObservedConn()
	secondaryStarted, releaseLoser := make(chan struct{}), make(chan struct{})
	defer func() {
		select {
		case <-releaseLoser:
		default:
			close(releaseLoser)
		}
	}()
	ips := []netip.Addr{netip.MustParseAddr("1.1.1.1"), netip.MustParseAddr("2606:4700:4700::1111")}
	conn, err := dialPublicIPs(ctx, "tcp", "443", ips, func(_ context.Context, _, address string) (net.Conn, error) {
		if address == "1.1.1.1:443" {
			<-secondaryStarted
			return winner, nil
		}
		close(secondaryStarted)
		// Simulate a successful connect already completing during cancellation.
		<-releaseLoser
		return loser, nil
	}, 0)
	if err != nil || conn != winner || winner.closes.Load() != 0 {
		t.Fatal("winner not transferred to caller", err)
	}
	_ = conn.Close()
	// Release the late result after the parent has returned.
	close(releaseLoser)
	waitDialSignal(t, loser.closed)
}

func TestPublicDialSharesDeadlineAcrossSameFamily(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	winner := newObservedConn()
	var calls int
	conn, err := dialPublicIPs(ctx, "tcp", "443", []netip.Addr{netip.MustParseAddr("1.1.1.1"), netip.MustParseAddr("8.8.8.8")}, func(attempt context.Context, _, _ string) (net.Conn, error) {
		calls++
		if calls == 1 {
			deadline, ok := attempt.Deadline()
			outer, _ := ctx.Deadline()
			if !ok || outer.Sub(deadline) < time.Second || time.Until(deadline) < 2*time.Second {
				t.Fatal("first address consumed budget reserved for later address")
			}
			return nil, context.DeadlineExceeded
		}
		if attempt.Err() != nil {
			t.Fatal("one failed address cancelled entire connection")
		}
		return winner, nil
	}, 0)
	if err != nil || calls != 2 {
		t.Fatal("later address not attempted", err)
	}
	_ = conn.Close()
}

func TestPublicDialFailureAndPreCancellationPreserveCauses(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	ips := []netip.Addr{netip.MustParseAddr("1.1.1.1"), netip.MustParseAddr("2606:4700:4700::1111")}
	if _, err := dialPublicIPs(ctx, "tcp", "443", ips, nil, 0); !errors.Is(err, context.Canceled) {
		t.Fatal("pre-cancelled connection tried to dial", err)
	}
	failure := &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("fixture network failure")}
	var calls atomic.Int32
	_, err := dialPublicIPs(context.Background(), "tcp", "443", ips, func(context.Context, string, string) (net.Conn, error) {
		calls.Add(1)
		return nil, failure
	}, time.Hour)
	if !errors.Is(err, failure) || calls.Load() != 2 {
		t.Fatal("all-family failure lost network cause or skipped fallback", err)
	}
}

func BenchmarkPublicDialSingleAddress(b *testing.B) {
	ips := []netip.Addr{netip.MustParseAddr("1.1.1.1")}
	conn := &observedConn{}
	dial := func(context.Context, string, string) (net.Conn, error) { return conn, nil }
	b.ReportAllocs()
	for b.Loop() {
		if _, err := dialPublicIPs(context.Background(), "tcp", "443", ips, dial, 300*time.Millisecond); err != nil {
			b.Fatal(err)
		}
	}
}
