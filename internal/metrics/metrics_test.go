package metrics

import (
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestRecorders(t *testing.T) {
	RecordTool("pods_list", "home", "ok", 2*time.Millisecond)
	RecordTool("pods_list", "", "error", time.Millisecond) // empty cluster -> "-"
	if v := testutil.ToFloat64(toolCalls.WithLabelValues("pods_list", "home", "ok")); v != 1 {
		t.Fatalf("tool ok = %v", v)
	}
	if v := testutil.ToFloat64(toolCalls.WithLabelValues("pods_list", "-", "error")); v != 1 {
		t.Fatalf("tool empty-cluster = %v", v)
	}

	RecordAuth("oidc", "allow")
	RecordAuth("none", "deny")
	if v := testutil.ToFloat64(authRequests.WithLabelValues("oidc", "allow")); v != 1 {
		t.Fatalf("auth allow = %v", v)
	}
	if v := testutil.ToFloat64(authRequests.WithLabelValues("none", "deny")); v != 1 {
		t.Fatalf("auth deny = %v", v)
	}

	RecordWriteBlocked("home", "cluster_readonly")
	if v := testutil.ToFloat64(writesBlocked.WithLabelValues("home", "cluster_readonly")); v != 1 {
		t.Fatalf("writes blocked = %v", v)
	}

	SetClusterUp("home", true)
	if v := testutil.ToFloat64(clusterUp.WithLabelValues("home")); v != 1 {
		t.Fatalf("cluster up = %v", v)
	}
	SetClusterUp("home", false)
	if v := testutil.ToFloat64(clusterUp.WithLabelValues("home")); v != 0 {
		t.Fatalf("cluster down = %v", v)
	}

	SetBuildInfo("v-test")
	// build_info has a goversion label; just assert the vec has a sample.
	if n := testutil.CollectAndCount(buildInfo); n < 1 {
		t.Fatalf("build_info samples = %d", n)
	}
}

func TestClientGoRegistrationDoesNotPanic(t *testing.T) {
	// Safe to call (client-go allows a single registration; this may no-op).
	RegisterClientGo()
}
