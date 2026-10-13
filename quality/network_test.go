package quality

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strings"
	"testing"

	"searchan/search"
)

func TestNetworkFailureSnapshotKeepsSafeCategoryAndReplaySemantics(t *testing.T) {
	for _, tc := range []struct {
		err  error
		code string
	}{
		{&net.DNSError{Err: "TOPSECRET-TEST", Name: "private.example", IsTimeout: true}, "network_timeout"},
		{&net.OpError{Op: "TOPSECRET-TEST", Net: "tcp", Err: errors.New("private.example")}, "network_error"},
	} {
		t.Run(tc.code, func(t *testing.T) {
			record := Record()
			record.save("search", "fixture", search.Response{}, fmt.Errorf("private wrapper: %w", tc.err))
			snapshot := record.Snapshot()
			encoded, err := json.Marshal(snapshot)
			if err != nil || len(snapshot.Exchanges) != 1 || snapshot.Exchanges[0].Error != tc.code || strings.Contains(string(encoded), "TOPSECRET") || strings.Contains(string(encoded), "private") {
				t.Fatal("network metadata lost category or retained raw details")
			}
			replay, err := Replay(snapshot)
			if err != nil {
				t.Fatal(err)
			}
			var response search.Response
			err = replay.load("search", "fixture", &response)
			var network net.Error
			if !errors.As(err, &network) || network.Timeout() != (tc.code == "network_timeout") || ErrorCode(err) != tc.code || replay.Misses() != 0 {
				t.Fatal("replay no longer exposes network failure to outage scheduling", err)
			}
		})
	}
}
