package reader

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"time"
)

type ipDial func(context.Context, string, string) (net.Conn, error)

// Validate the entire DNS answer before contacting any address, then use only
// those literal IPs. Two address families may race; each family stays serial.
// This retains the reader's destination policy without resolving a host again.
func dialPublicIPs(ctx context.Context, network, port string, ips []netip.Addr, dial ipDial, delay time.Duration) (net.Conn, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if network != "tcp" && network != "tcp4" && network != "tcp6" {
		return nil, errors.New("unsupported page connection network")
	}
	mixed := false
	for i, ip := range ips {
		if !PublicIP(ip) || ip.Zone() != "" {
			return nil, errors.New("hostname resolves to non-public address")
		}
		mixed = mixed || i > 0 && ip.Unmap().Is4() != ips[0].Unmap().Is4()
	}
	if len(ips) == 0 {
		return nil, errors.New("hostname has no usable IP addresses")
	}
	if network == "tcp" && !mixed {
		return dialIPList(ctx, network, port, ips, dial)
	}
	primary := make([]netip.Addr, 0, len(ips))
	var secondary []netip.Addr
	for _, ip := range ips {
		ip = ip.Unmap()
		if network == "tcp4" && !ip.Is4() || network == "tcp6" && ip.Is4() {
			continue
		}
		if len(primary) == 0 || ip.Is4() == primary[0].Is4() {
			primary = append(primary, ip)
		} else {
			secondary = append(secondary, ip)
		}
	}
	if len(primary) == 0 {
		return nil, errors.New("hostname has no usable IP addresses")
	}
	if len(secondary) == 0 {
		return dialIPList(ctx, network, port, primary, dial)
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	type result struct {
		conn net.Conn
		err  error
	}
	// An unbuffered handoff transfers connection ownership to the caller. After
	// return, workers close even a connection established just before cancel.
	results, returned := make(chan result), make(chan struct{})
	defer close(returned)
	start := func(group []netip.Addr) {
		go func() {
			conn, err := dialIPList(ctx, network, port, group, dial)
			select {
			case results <- result{conn, err}:
			case <-returned:
				if conn != nil {
					_ = conn.Close()
				}
			}
		}()
	}
	start(primary)
	active, pending := 1, true
	timer := time.NewTimer(delay)
	defer timer.Stop()
	var last error
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-timer.C:
			if pending {
				start(secondary)
				active++
				pending = false
			}
		case r := <-results:
			if r.err == nil {
				return r.conn, nil
			}
			last = r.err
			active--
			if pending {
				start(secondary) // Do not wait out the delay after an early failure.
				active++
				pending = false
			}
			if active == 0 {
				return nil, last
			}
		}
	}
}

func dialIPList(ctx context.Context, network, port string, ips []netip.Addr, dial ipDial) (net.Conn, error) {
	var last error
	for i, ip := range ips {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		attempt, cancel := ctx, func() {}
		if deadline, ok := ctx.Deadline(); ok && i+1 < len(ips) {
			remaining := time.Until(deadline)
			// Reserve time for later addresses without imposing sub-second TCP
			// timeouts when the outer budget can afford a normal attempt.
			share := min(remaining, max(2*time.Second, remaining/time.Duration(len(ips)-i)))
			attempt, cancel = context.WithDeadline(ctx, time.Now().Add(share))
		}
		conn, err := dial(attempt, network, net.JoinHostPort(ip.Unmap().String(), port))
		cancel()
		if err == nil {
			return conn, nil
		}
		last = err
	}
	return nil, last
}
