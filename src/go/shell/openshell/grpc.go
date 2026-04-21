// Native-gRPC helpers for the OpenShell driver.
//
// This file owns:
//
//   - Lazy gRPC dial + OpenShellClient construction.
//   - Policy -> *pb.SandboxSpec translation (matching Python's
//     _build_sandbox_spec).
//   - CreateSandbox / DeleteSandbox lifecycle RPCs.
//   - ExecSandbox stream draining. The server-streaming API is consumed
//     twice — once in aggregating mode (Exec) and once in forwarding
//     mode (ExecStream) — so the low-level receive loop is factored
//     out as drainStream and both callers supply a per-event sink.
package openshell

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"

	pb "agent-harness/go/proto/openshell"
	"agent-harness/go/shell"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// dialClient opens an insecure gRPC channel to endpoint and returns an
// OpenShellClient stub. The caller is responsible for closing the channel
// — we track it on the Driver so Close() can shut it down.
func dialClient(ctx context.Context, endpoint string) (pb.OpenShellClient, *grpc.ClientConn, error) {
	conn, err := grpc.NewClient(
		endpoint,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		return nil, nil, fmt.Errorf("openshell: dial %q: %w", endpoint, err)
	}
	return pb.NewOpenShellClient(conn), conn, nil
}

// toSandboxSpec translates a shell.SecurityPolicy into the proto
// SandboxSpec used by CreateSandbox. Nil policy yields an empty-but-
// valid spec (permissive defaults).
func toSandboxSpec(policy *shell.SecurityPolicy, workspace string) *pb.SandboxSpec {
	fsPolicy := &pb.FilesystemPolicy{}
	netPolicies := []*pb.NetworkPolicy{}
	infPolicy := &pb.InferencePolicy{}

	if policy != nil {
		if len(policy.FilesystemAllow) > 0 {
			fsPolicy.ReadWrite = append([]string(nil), policy.FilesystemAllow...)
		}
		for _, rule := range policy.NetworkRules {
			action := pb.NetworkPolicy_ALLOW
			if rule.Action == shell.DenyAction {
				action = pb.NetworkPolicy_DENY
			}
			netPolicies = append(netPolicies, &pb.NetworkPolicy{
				Cidr:   rule.CIDR,
				Action: action,
			})
		}
		infPolicy.RoutingEnabled = policy.Inference.RoutingEnabled
	}

	return &pb.SandboxSpec{
		Policy: &pb.SandboxPolicy{
			Filesystem:      fsPolicy,
			NetworkPolicies: netPolicies,
			Inference:       infPolicy,
		},
		Workspace: workspace,
	}
}

// randomSandboxName returns a short unique suffix used when auto-creating
// sandboxes ("harness-<4 random bytes hex>"). Matches Python's
// f"harness-{os.urandom(4).hex()}" naming.
func randomSandboxName() string {
	var buf [4]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "harness-unknown"
	}
	return "harness-" + hex.EncodeToString(buf[:])
}

// execSink is invoked by drainStream for each non-nil event (one of
// stdout/stderr/exit). Returning a non-nil error aborts the drain.
type execSink func(event *pb.ExecSandboxEvent) error

// drainStream pulls events off a server-streaming ExecSandbox call,
// forwarding each to sink. The loop exits cleanly on io.EOF (stream
// closed normally). Any other transport error is returned.
func drainStream(stream grpc.ServerStreamingClient[pb.ExecSandboxEvent], sink execSink) error {
	for {
		event, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("openshell: recv exec event: %w", err)
		}
		if event == nil {
			continue
		}
		if err := sink(event); err != nil {
			return err
		}
	}
}
