package util_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"agent-harness/go/internal/util"
)

func TestSnakeCase(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"empty", "", ""},
		{"single lowercase", "a", "a"},
		{"single uppercase", "A", "a"},
		{"all lowercase word", "foo", "foo"},
		{"camelCase", "webBrowsing", "web_browsing"},
		{"PascalCase", "WebBrowsing", "web_browsing"},
		{"consecutive caps HTTPClient", "HTTPClient", "httpclient"},
		{"consecutive caps URLPath", "URLPath", "urlpath"},
		{"numeric mid-word Api2Client", "Api2Client", "api2_client"},
		{"trailing Skill suffix", "WebBrowsingSkill", "web_browsing"},
		{"only-suffix-is-Skill", "Skill", ""},
		{"Skill suffix with single word", "FooSkill", "foo"},
		{"multiple words PascalCase", "MyLongClassName", "my_long_class_name"},
		{"digit-then-lower", "Foo1bar", "foo1bar"},
		{"digit-then-upper", "Foo1Bar", "foo1_bar"},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			got := util.SnakeCase(tc.in)
			assert.Equal(t, tc.want, got)
		})
	}
}
