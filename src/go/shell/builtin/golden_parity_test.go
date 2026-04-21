package builtin_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"agent-harness/go/shell"
	"agent-harness/go/shell/builtin"
	"agent-harness/go/shell/vfs"
)

// goldenCase is one row in testdata/golden.json. Each case captures
// exactly what the Python reference shell produces for a canonical
// command, so we can verify the Go port produces the same output,
// stderr, and exit code.
//
// The fixture seeds a small virtual filesystem before each case runs;
// see newGoldenDriver for the layout.
type goldenCase struct {
	Name             string `json:"name"`
	Command          string `json:"command"`
	ExpectedStdout   string `json:"expected_stdout"`
	ExpectedStderr   string `json:"expected_stderr"`
	ExpectedExitCode int    `json:"expected_exit_code"`
}

// newGoldenDriver constructs the standard fixture driver used by the
// golden parity harness. It mirrors TestShell.setup_method in
// tests/python/test_shell.py:
//
//   - /data/hello.txt → "hello world\n"
//   - /data/nums.txt  → "3\n1\n2\n1\n"
//   - /data/users.json → JSON list of {name, role} objects
//
// We also register an `errcmd` user command used by the stderr-
// redirect cases — Python's tests register the same stub locally.
func newGoldenDriver(t *testing.T) *builtin.BuiltinShellDriver {
	t.Helper()
	fs := vfs.NewBuiltinFilesystemDriver()
	require.NoError(t, fs.WriteString("/data/hello.txt", "hello world\n"))
	require.NoError(t, fs.WriteString("/data/nums.txt", "3\n1\n2\n1\n"))
	require.NoError(t, fs.WriteString("/data/users.json",
		"[\n  {\n    \"name\": \"Alice\",\n    \"role\": \"admin\"\n  },\n  {\n    \"name\": \"Bob\",\n    \"role\": \"user\"\n  }\n]"))

	d := builtin.NewBuiltinShellDriver(builtin.WithFS(fs))
	d.RegisterCommand("errcmd", func(_ []string, _ string) shell.ExecResult {
		return shell.ExecResult{Stdout: "out\n", Stderr: "err\n", ExitCode: 0}
	})
	return d
}

// loadGoldenCases reads and decodes testdata/golden.json. Test fails
// if the file is missing or malformed — the fixture is the source of
// truth, not optional.
func loadGoldenCases(t *testing.T) []goldenCase {
	t.Helper()
	path := filepath.Join("testdata", "golden.json")
	data, err := os.ReadFile(path)
	require.NoError(t, err, "missing fixture: %s", path)
	var cases []goldenCase
	require.NoError(t, json.Unmarshal(data, &cases), "malformed JSON in %s", path)
	require.NotEmpty(t, cases, "golden fixture must contain at least one case")
	return cases
}

// TestGoldenParity runs each fixture case through a fresh shell and
// asserts exact-match stdout/stderr/exit. Fresh driver per case keeps
// side effects (cd, redirections to /tmp/*) from bleeding into
// subsequent cases.
//
// The expected values were hand-authored against the virtual-bash
// reference documented at docs/guides/virtual-bash-reference.md and
// cross-referenced with tests/python/test_shell.py. If a Go change
// causes divergence from Python, this harness flags it as a test
// failure and the diff points directly at the rule being violated.
func TestGoldenParity(t *testing.T) {
	cases := loadGoldenCases(t)

	for _, c := range cases {
		c := c
		t.Run(c.Name, func(t *testing.T) {
			d := newGoldenDriver(t)
			res, err := d.Exec(context.Background(), c.Command)
			require.NoError(t, err, "unexpected driver error for %q", c.Command)

			assert.Equal(t, c.ExpectedStdout, res.Stdout,
				"stdout mismatch for case %q (command=%q)", c.Name, c.Command)
			assert.Equal(t, c.ExpectedStderr, res.Stderr,
				"stderr mismatch for case %q (command=%q)", c.Name, c.Command)
			assert.Equal(t, c.ExpectedExitCode, res.ExitCode,
				"exit code mismatch for case %q (command=%q)", c.Name, c.Command)
		})
	}
}
