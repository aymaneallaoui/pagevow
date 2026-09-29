package verify

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/aymaneallaoui/pagevow/internal/page"
)

type goldenCase struct {
	Name     string      `json:"name"`
	Verifier string      `json:"verifier"`
	Final    page.State  `json:"final"`
	Initial  *page.State `json:"initial"`
	ArgsYAML string      `json:"args_yaml"`
	Error    *string     `json:"error"`
	Result   *struct {
		Passed         bool            `json:"passed"`
		Checks         [][]interface{} `json:"checks"`
		Expected       *string         `json:"expected"`
		HasExpected    bool            `json:"-"`
		VisibleFlights []string        `json:"visible_flights"`
	} `json:"result"`
	Explain []string `json:"explain"`
}

func TestGoldenCasesMatchPython(t *testing.T) {
	var cases []goldenCase
	loadFixture(t, "cases.json", &cases)
	require.GreaterOrEqual(t, len(cases), 40)
	kinds := map[string]int{}
	for _, tc := range cases {
		kinds[tc.Verifier]++
		t.Run(tc.Name, func(t *testing.T) {
			v, ok := Lookup(tc.Verifier)
			require.True(t, ok)
			args, err := v.Decode(yamlNode(t, tc.ArgsYAML), testEnv())
			var res Result
			if err == nil {
				res, err = v.Verify(tc.Final, tc.Initial, args)
			}
			if tc.Error != nil {
				require.Error(t, err, "python raised %s", *tc.Error)
				assert.Contains(t, strings.ToLower(err.Error()), "return_")
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.Result.Passed, res.Passed)
			require.Len(t, res.Names(), len(tc.Result.Checks))
			for i, pair := range tc.Result.Checks {
				assert.Equal(t, pair[0], res.Names()[i], "check order")
				assert.Equal(t, pair[1], res.Checks[pair[0].(string)], "check %v", pair[0])
			}
			if tc.Verifier == "hn_story" {
				assert.Equal(t, tc.Result.Expected, res.Expected)
			}
			if tc.Verifier == "flights" {
				assert.Equal(t, tc.Result.VisibleFlights, res.VisibleFlights)
			}
			assert.Equal(t, tc.Explain, Explain(tc.Final, res, args))
		})
	}
	for _, name := range Names() {
		assert.GreaterOrEqual(t, kinds[name], 3, name)
	}
}
