package sidebar

import (
	"slices"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kriuchkov/herdr-scope/internal/domain"
	"github.com/kriuchkov/herdr-scope/internal/ports"
)

func presentationFor(variant ports.Variant, layout ports.Layout) Presentation {
	return Presentation{Glyphs: NewGlyphs(ports.PresentationSettings{Variant: variant}, nil), Layout: layout}
}

func TestLayoutParsesAsTOMLAndRespectsHerdrLimits(t *testing.T) {
	for _, variant := range []ports.Variant{ports.VariantText, ports.VariantFont, ports.VariantNone} {
		for _, appearance := range []ports.Appearance{ports.AppearanceLight, ports.AppearanceDark} {
			for _, kind := range []ports.Layout{ports.LayoutCompact, ports.LayoutClassic} {
				testLayoutParses(t, presentationFor(variant, kind), appearance)
			}
		}
	}
}

func testLayoutParses(t *testing.T, p Presentation, appearance ports.Appearance) {
	t.Helper()
	layout, err := NewLayout(p, appearance, []domain.AgentKind{"claude", "gemini", "codex"})
	require.NoError(t, err)
	text := layout.TOML()
	var parsed map[string]any
	_, err = toml.Decode(text, &parsed)
	require.NoError(t, err, "%s %s %s:\n%s", p.Glyphs.Variant(), p.Layout, appearance, text)

	ui := parsed["ui"].(map[string]any)["sidebar"].(map[string]any)
	agents := ui["agents"].(map[string]any)
	rows := agents["rows"].([]any)
	assert.LessOrEqual(t, len(rows), 16)
	for _, row := range rows {
		assert.LessOrEqual(t, len(row.([]any)), 16)
	}
	byAgent := agents["rows_by_agent"].(map[string]any)
	assert.Contains(t, byAgent, "claude")
	assert.NotContains(t, byAgent, "codex", "a vendor without a hue needs no override row")
	for _, row := range ui["spaces"].(map[string]any)["rows"].([]any) {
		assert.LessOrEqual(t, len(row.([]any)), 16, "the Spaces row is at its limit by design")
	}
	got, ok := BlockVariant(text)
	assert.True(t, ok)
	assert.Equal(t, p.Glyphs.Variant(), got)
	assert.Contains(t, text, LayoutTag+string(p.Layout))
}

func TestLayoutColoursFollowTheAppearance(t *testing.T) {
	p := presentationFor(ports.VariantText, ports.LayoutClassic)
	light, err := NewLayout(p, ports.AppearanceLight, nil)
	require.NoError(t, err)
	dark, err := NewLayout(p, ports.AppearanceDark, nil)
	require.NoError(t, err)
	assert.Contains(t, light.TOML(), `token = "$title_idle_stale", fg = "#9a9da8"`)
	assert.Contains(t, dark.TOML(), `token = "$title_idle_stale", fg = "#6b6f82"`)
	assert.Contains(t, dark.TOML(), `token = "$title_idle_fresh", fg = "#c8cde0"`, "fresh idle is bright, not green")
	assert.Contains(t, light.TOML(), `token = "$space_label", fg = "#5c5f77"`, "a name reads on a light panel")
	assert.Contains(t, dark.TOML(), `token = "$space_label", fg = "#9a9eb3"`)
	assert.Contains(t, light.TOML(), `fg = "#16161c", bold = false, dim = false, rules = [{ contains = "§", fg = "#d97757" }`)
	assert.Contains(t, light.TOML(), `token = "$title_working", fg = "#c78a1f", bold = true`, "the fallback row works in other's colour")
	for token, fg := range map[string]string{"$logo_fresh": "#c8cde0", "$logo_idle": "#9499ad", "$logo_unknown": "#907aa9"} {
		assert.Contains(t, dark.TOML(), `token = "`+token+`", fg = "`+fg+`"`, "an idle logo takes its title's colour")
	}
	assert.Contains(t, dark.TOML(), `token = "$space_logo_idle", fg = "#8f95ab"`)
}

func TestWorkingTitleWearsItsVendorsColour(t *testing.T) {
	layout, err := NewLayout(presentationFor(ports.VariantText, ports.LayoutClassic), ports.AppearanceLight, []domain.AgentKind{"gemini"})
	require.NoError(t, err)
	row := layout.RowsFor("gemini")[2]
	i := slices.IndexFunc(row, func(c Cell) bool { return c.Token == "$title_working" })
	require.GreaterOrEqual(t, i, 0)
	assert.Equal(t, "#4285f4", row[i].Fg)
	assert.Equal(t, layout.Agents, layout.RowsFor("droid"), "anything else uses the fallback rows")

	compact, err := NewLayout(presentationFor(ports.VariantText, ports.LayoutCompact), ports.AppearanceLight, []domain.AgentKind{"gemini"})
	require.NoError(t, err)
	row = compact.RowsFor("gemini")[2]
	assert.Equal(t, "$title_working", row[0].Token)
	assert.Equal(t, "#4285f4", row[0].Fg, "the whole working line wears the vendor's colour")
}

func TestCompactLayoutKeepsEveryLineInOneCell(t *testing.T) {
	layout, err := NewLayout(presentationFor(ports.VariantText, ports.LayoutCompact), ports.AppearanceDark, nil)
	require.NoError(t, err)
	for _, c := range layout.Agents[2] {
		assert.True(t, strings.HasPrefix(c.Token, "$title_"), "only state cells, one of which is set: %s", c.Token)
	}
	require.Len(t, layout.Spaces, 1, "a workspace is one line")
	for _, c := range layout.Spaces[0] {
		assert.NotEqual(t, "$space_label", c.Token, "the name rides in the mark's cell")
	}
	assert.NotContains(t, layout.TOML(), "rules = [", "no logo cell to colour by rule")
}

func TestNoneVariantHasNoLogoRules(t *testing.T) {
	layout, err := NewLayout(presentationFor(ports.VariantNone, ports.LayoutClassic), ports.AppearanceLight, nil)
	require.NoError(t, err)
	assert.NotContains(t, layout.TOML(), "contains = ", "only the fold count's own rule is left")
	assert.Contains(t, layout.TOML(), VariantTag+"none")
}

func TestRender(t *testing.T) {
	layout, err := NewLayout(presentationFor(ports.VariantText, ports.LayoutClassic), ports.AppearanceLight, nil)
	require.NoError(t, err)
	store := Store{}
	store.Apply("p1", map[string]*string{"group": new("dashboard"), "logo": new("§"), "title_done": new("✓ Cache search results")})
	store.Apply(
		"p2",
		map[string]*string{"logo": new(zwsp + "  Λ"), "title_idle": new("Fix"), "gap": new(zwsp), "empty": new("  ")},
	)
	out := Render([]Entry{{Rows: layout.Agents, Tokens: store["p1"]}, {Rows: layout.Agents, Tokens: store["p2"]}}, RenderOptions{})
	assert.Equal(t, " dashboard\n   § · ✓ Cache search results\n   Λ · Fix\n\n", out, "the gap row draws as a blank line")
	annotated := Render([]Entry{{Rows: layout.Agents, Tokens: store["p1"]}}, RenderOptions{Annotate: true})
	assert.Contains(t, annotated, "✓ Cache search results{title_done}")
	assert.NotContains(t, store["p2"], "empty", "an empty value is a clear")
}

func TestNormalizeMatchesHerdr(t *testing.T) {
	v, ok := Normalize("  review\nready  ")
	assert.True(t, ok)
	assert.Equal(t, "reviewready", v)
	_, ok = Normalize(" \n ")
	assert.False(t, ok)
	v, _ = Normalize(zwsp + "  x")
	assert.Equal(t, zwsp+"  x", v, "a zero-width space survives the trim")
	v, _ = Normalize(strings.Repeat("a", 100))
	assert.Len(t, v, 80)
}

type blockSpyLogger struct {
	debugs []string
	warns  []string
}

func (l *blockSpyLogger) Debug(msg string, _ ...any) { l.debugs = append(l.debugs, msg) }
func (l *blockSpyLogger) Info(string, ...any)        {}
func (l *blockSpyLogger) Warn(msg string, _ ...any)  { l.warns = append(l.warns, msg) }
func (l *blockSpyLogger) Error(string, ...any)       {}

func TestRunLinePrependsInCompactSkipsAndLogsOnceInClassicAndOffIsIdentical(t *testing.T) {
	baseSettings := ports.DefaultSettings()
	baseSettings.Presentation.Variant = ports.VariantText
	baseSettings.Presentation.Layout = ports.LayoutCompact
	offWithoutRoot, err := NewLayout(NewPresentation(baseSettings, nil), ports.AppearanceDark, []domain.AgentKind{"claude"})
	require.NoError(t, err)

	withRootOff := baseSettings
	withRootOff.RunRoot = "/tmp/runs"
	withRootOff.Presentation.RunLine = ports.RunLineOff
	offWithRoot, err := NewLayout(NewPresentation(withRootOff, nil), ports.AppearanceDark, []domain.AgentKind{"claude"})
	require.NoError(t, err)
	assert.Equal(t, offWithoutRoot.TOML(), offWithRoot.TOML(), "run_line=off keeps TOML byte-identical even with run_root set")
}

// TestRunLineTakesAsManyCellsAsFitHighestValueFirst pins the adaptive prepend: cells go on highest value first
// and stop at Herdr's ceiling, so a layout that cannot hold all three keeps
// the two that matter instead of losing every one.
func TestRunLineTakesAsManyCellsAsFitHighestValueFirst(t *testing.T) {
	cases := []struct {
		name   string
		layout ports.Layout
		// want is the run cells prepended, in order.
		want []string
		// lineLen is the cell count the layout already spends on furniture,
		// titles and logos.
		lineLen int
	}{
		// Seven title cells leave room for all three (3 + 7 = 10 ≤ 16).
		{"compact takes all three", ports.LayoutCompact, []string{"$run_state", "$run_age", "$run_signal"}, 7},
		// Fourteen furniture, logo and title cells leave room for two; the
		// signal goes (2 + 14 = 16, a third would be 17).
		{"classic takes two", ports.LayoutClassic, []string{"$run_state", "$run_age"}, 14},
		// A line already at the ceiling keeps none and only logs.
		{"a full line takes none", ports.LayoutClassic, nil, rowTokenLimit},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			settings := ports.DefaultSettings()
			settings.Presentation.Variant = ports.VariantText
			settings.Presentation.Layout = ports.LayoutCompact
			settings.RunRoot = "/tmp/runs"
			settings.Presentation.RunLine = ports.RunLineOn
			settings.Presentation.Layout = tc.layout
			p := NewPresentation(settings, nil)
			logger := &blockSpyLogger{}
			p.Logger = logger

			line := make([]Cell, tc.lineLen)
			for i := range line {
				line[i] = cell("$title_idle", "#ffffff", false)
			}
			got := runLineCells(line, colorsFor(ports.AppearanceDark), p)
			require.Len(t, got, len(tc.want))
			for i, want := range tc.want {
				assert.Equal(t, want, got[i].Token)
			}
			assert.LessOrEqual(t, len(got)+len(line), rowTokenLimit)

			dropped := 3 - len(tc.want)
			if dropped == 0 {
				assert.Empty(t, logger.debugs, "nothing dropped, nothing logged")
				return
			}
			assert.Len(t, logger.debugs, 1, "the drop is logged once, at debug level")
			assert.Empty(t, logger.warns, "a degraded row is not a warning")
		})
	}
}

// TestRunLineLayouts proves the real layouts get the predicted counts and that
// the run cells keep their colours.
func TestRunLineLayoutsKeepTheirColorsAndCounts(t *testing.T) {
	settings := ports.DefaultSettings()
	settings.Presentation.Variant = ports.VariantText
	settings.RunRoot = "/tmp/runs"
	settings.Presentation.RunLine = ports.RunLineOn

	settings.Presentation.Layout = ports.LayoutCompact
	compact, err := NewLayout(NewPresentation(settings, nil), ports.AppearanceDark, []domain.AgentKind{"claude"})
	require.NoError(t, err)
	assert.Contains(t, compact.TOML(), RunLineTag)
	assert.Len(t, compact.Agents[2], 10)
	assert.Equal(t, "$run_state", compact.Agents[2][0].Token)
	assert.Equal(t, "$run_age", compact.Agents[2][1].Token)
	assert.Equal(t, "$run_signal", compact.Agents[2][2].Token)
	assert.Equal(t, "$title_working", compact.Agents[2][3].Token)
	c := colorsFor(ports.AppearanceDark)
	assert.Equal(t, c.Subtle, compact.Agents[2][0].Fg, "run_state keeps its colour")
	assert.Equal(t, c.IdleStale, compact.Agents[2][1].Fg, "run_age keeps its colour")
	assert.Equal(t, c.Subtle, compact.Agents[2][2].Fg, "run_signal keeps its colour")

	settings.Presentation.Layout = ports.LayoutClassic
	p := NewPresentation(settings, nil)
	logger := &blockSpyLogger{}
	p.Logger = logger
	classic, err := NewLayout(p, ports.AppearanceDark, []domain.AgentKind{"claude", "gemini"})
	require.NoError(t, err)
	assert.Len(t, classic.Agents[2], 16, "classic keeps its ten furniture cells and gains two run cells")
	assert.Equal(t, "$run_state", classic.Agents[2][0].Token)
	assert.Equal(t, "$run_age", classic.Agents[2][1].Token)
	assert.Equal(t, "$split_mark", classic.Agents[2][2].Token, "every other cell keeps its place")
	assert.Len(t, logger.debugs, 1, "the dropped signal is logged once across agent rows")
	assert.Empty(t, logger.warns)
}
