//go:build darwin

package main

// Hermetic tests for the AI 用量 page: fake quota fetches and a fake local
// scanner behind the same usageDeps the feature injects, goWork/dispatchMain
// stubbed to run inline (or queued where a test wants an in-flight scan).
// The only non-hermetic test is the screenshot, gated by SHELF_BASKET_SMOKE.

import (
	"context"
	"image"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"shelf/internal/aiusage"
	"shelf/internal/settings"
)

type fakeQuotaFetch struct {
	calls             int
	q                 aiusage.Quota
	qs                []aiusage.Quota
	cancelledAt       int  // calls index at which ctx was already cancelled
	answerAfterCancel bool // a server answer already received before cancellation
}

func (f *fakeQuotaFetch) fetch(ctx context.Context) aiusage.Quota {
	q := f.q
	if len(f.qs) > 0 {
		q = f.qs[0]
		if len(f.qs) > 1 {
			f.qs = f.qs[1:]
		}
	}
	f.calls++
	if ctx.Err() != nil {
		f.cancelledAt = f.calls
		if !f.answerAfterCancel {
			return aiusage.Quota{Status: aiusage.QuotaFailed, Err: ctx.Err().Error()}
		}
	}
	return q
}

// fakeUsageScanner records Scan calls and every range Summary is asked for.
type fakeUsageScanner struct {
	scans     int
	ranges    []aiusage.Range
	summaries map[aiusage.Range]aiusage.Summary
	err       error
	cancelled bool // a Scan saw its ctx already cancelled
}

func (s *fakeUsageScanner) Scan(ctx context.Context, progress func(done, total int64)) error {
	s.scans++
	if ctx.Err() != nil {
		s.cancelled = true
	}
	return s.err
}

func (s *fakeUsageScanner) Summary(r aiusage.Range, now time.Time) aiusage.Summary {
	s.ranges = append(s.ranges, r)
	if s.summaries != nil {
		if sum, ok := s.summaries[r]; ok {
			return sum
		}
	}
	return aiusage.Summary{Range: r}
}

// stubUsageSeams replaces goWork and dispatchMain: work runs inline so a
// tick completes the whole fetch→publish chain, unless queued is wanted.
func stubUsageSeams(t *testing.T, queued bool) (flush func()) {
	t.Helper()
	origWork, origMain := goWork, dispatchMain
	var queue []func()
	if queued {
		goWork = func(fn func()) { queue = append(queue, fn) }
		dispatchMain = func(fn func()) { queue = append(queue, fn) }
		flush = func() {
			for len(queue) > 0 {
				fn := queue[0]
				queue = queue[1:]
				fn()
			}
		}
	} else {
		goWork = func(fn func()) { fn() }
		dispatchMain = func(fn func()) { fn() }
	}
	t.Cleanup(func() {
		goWork, dispatchMain = origWork, origMain
	})
	return flush
}

// usageFixture wires a feature with fake deps onto a visible shelf and
// returns the lot for assertions.
type usageFixture struct {
	a      *shelf
	f      *usageFeature
	view   *ui.Tester
	now    *time.Time
	codex  *fakeQuotaFetch
	claude *fakeQuotaFetch
	scan   *fakeUsageScanner
}

func newUsageFixture(t *testing.T, on bool) *usageFixture {
	return newUsageFixtureWith(t, on, nil)
}

// newUsageFixtureWith is newUsageFixture with a chance to change the
// injected dependencies (persistence, the Claude schedule).
func newUsageFixtureWith(t *testing.T, on bool, tweak func(*usageDeps)) *usageFixture {
	t.Helper()
	stubUsageSeams(t, false)
	dir := t.TempDir()
	now := time.Date(2026, 10, 19, 12, 0, 0, 0, time.Local)
	a := &shelf{dataDir: dir, visible: true, settings: settings.Default(),
		thumbnails: map[string]*ui.Bitmap{}, thumbPending: map[string]bool{}, appNames: map[string]string{}}
	a.settings.AIUsage.Enabled = on
	fx := &usageFixture{a: a, now: &now, codex: &fakeQuotaFetch{}, claude: &fakeQuotaFetch{}, scan: &fakeUsageScanner{}}
	deps := usageDeps{
		fetchCodex:  fx.codex.fetch,
		fetchClaude: fx.claude.fetch,
		pace:        fakePace,
		newScanner:  func() usageScanner { return fx.scan },
		// Deterministic Claude schedule: five whole minutes, as the low end
		// of the real 5–15 minute draw.
		claudeWait: func() time.Duration { return 5 * time.Minute },
		now:        func() time.Time { return *fx.now },
	}
	if tweak != nil {
		tweak(&deps)
	}
	fx.f = newUsageFeature(a, deps)
	a.registerPage(fx.f)
	fx.view = ui.NewTester(a.view, sheetW, sheetH)
	return fx
}

// fakePace is the even-pace seam: 40% expected remaining, always lasting
// until reset.
func fakePace(w aiusage.Window, now time.Time) aiusage.PaceInfo {
	return aiusage.PaceInfo{ExpectedLeftPct: 40, LastsUntilReset: true}
}

// okQuota is a healthy quota answer: 5% used on the 5-hour window, 30% on
// the weekly one.
func okQuota(tool aiusage.Tool, plan string, now time.Time) aiusage.Quota {
	return aiusage.Quota{
		Tool:      tool,
		Plan:      plan,
		Status:    aiusage.QuotaOK,
		FetchedAt: now,
		Windows: []aiusage.Window{
			{Name: "5 hours", UsedPct: 5, Length: 5 * time.Hour, ResetsAt: now.Add(4*time.Hour + 25*time.Minute)},
			{Name: "7 days", UsedPct: 30, Length: 7 * 24 * time.Hour, ResetsAt: now.Add(30 * time.Hour)},
		},
	}
}

// windowUsed sets one window's used share on a fixture answer.
func windowUsed(q aiusage.Quota, index int, used float64) aiusage.Quota {
	q.Windows[index].UsedPct = used
	return q
}

func TestUsageDisabledShowsNothingAndFetchesNothing(t *testing.T) {
	fx := newUsageFixture(t, false)
	fx.f.tick(*fx.now)
	fx.view.Frame()
	if _, found := fx.view.Find("AI 用量"); found {
		t.Fatal("AI 用量 ear icon must not show while the feature is off")
	}
	if fx.codex.calls != 0 || fx.claude.calls != 0 || fx.scan.scans != 0 {
		t.Fatalf("disabled feature must not fetch or scan: %d %d %d",
			fx.codex.calls, fx.claude.calls, fx.scan.scans)
	}
	// Even while hidden the tick must stay idle.
	for i := range 10 {
		fx.f.tick(fx.now.Add(time.Duration(i) * time.Hour))
	}
	if fx.codex.calls != 0 || fx.scan.scans != 0 {
		t.Fatal("tick on a disabled feature did work")
	}
}

func TestUsagePageSwitchTriggersOneFetchAndScan(t *testing.T) {
	fx := newUsageFixture(t, true)
	fx.codex.q = okQuota(aiusage.ToolCodex, "Plus", *fx.now)
	fx.claude.q = okQuota(aiusage.ToolClaude, "Pro", *fx.now)
	if _, found := fx.view.Find("AI 用量"); !found {
		t.Fatal("AI 用量 ear icon missing while the feature is on")
	}
	if err := fx.view.Click("AI 用量"); err != nil {
		t.Fatalf("switch to AI 用量: %v", err)
	}
	fx.f.tick(*fx.now)
	if fx.codex.calls != 1 || fx.claude.calls != 1 {
		t.Fatalf("one fetch per enabled quota tool expected, got %d %d", fx.codex.calls, fx.claude.calls)
	}
	if fx.scan.scans != 1 {
		t.Fatalf("one local scan expected, got %d", fx.scan.scans)
	}
	fx.view.Frame()
	for _, want := range []string{"Codex", "Plus", "Claude Code", "Pro", "5%", "4:25 后", "本机用量", "刚刚更新"} {
		if !fx.view.HasText(want) {
			t.Fatalf("page missing %q; texts=%v", want, fx.view.Texts())
		}
	}
	// Staying visible does not refetch before the intervals pass.
	for i := 1; i <= 3; i++ {
		fx.f.tick(fx.now.Add(time.Duration(i) * 15 * time.Second))
	}
	if fx.codex.calls != 1 || fx.scan.scans != 1 {
		t.Fatal("early refetch while visible")
	}
	// Local usage refreshes after its 60s, quota only after 5 minutes.
	fx.f.tick(fx.now.Add(61 * time.Second))
	if fx.scan.scans != 2 {
		t.Fatalf("local refresh after 60s expected, scans=%d", fx.scan.scans)
	}
	if fx.codex.calls != 1 {
		t.Fatal("quota must not refetch before 5 minutes")
	}
	fx.f.tick(fx.now.Add(5 * time.Minute))
	if fx.codex.calls != 2 || fx.claude.calls != 2 {
		t.Fatalf("quota refetch after 5 minutes expected, got %d %d", fx.codex.calls, fx.claude.calls)
	}
}

func TestUsageRefreshButtonForcesWithinLimits(t *testing.T) {
	fx := newUsageFixture(t, true)
	fx.codex.q = okQuota(aiusage.ToolCodex, "Plus", *fx.now)
	fx.claude.q = okQuota(aiusage.ToolClaude, "Pro", *fx.now)
	fx.view.Click("AI 用量")
	fx.f.tick(*fx.now)
	fx.view.Frame()
	// A manual refresh inside the same minute still only scans locally.
	if err := fx.view.Click("刷新"); err != nil {
		t.Fatalf("refresh button: %v", err)
	}
	if fx.codex.calls != 1 || fx.claude.calls != 1 || fx.scan.scans != 2 {
		t.Fatalf("manual refresh: codex=%d claude=%d scans=%d (want 1, 1, 2)",
			fx.codex.calls, fx.claude.calls, fx.scan.scans)
	}
	// At +31s Claude Code's 30-second floor has passed; Codex's 60-second
	// manual gap has not.
	*fx.now = fx.now.Add(31 * time.Second)
	fx.view.Click("刷新")
	if fx.codex.calls != 1 || fx.claude.calls != 2 {
		t.Fatalf("manual refresh at 31s: codex=%d claude=%d (want 1, 2)", fx.codex.calls, fx.claude.calls)
	}
	// At +61s Codex may be asked too.
	fx.codex.q = aiusage.Quota{Tool: aiusage.ToolCodex, Status: aiusage.QuotaRateLimited,
		FetchedAt: *fx.now, RetryAfter: 90 * time.Second}
	*fx.now = fx.now.Add(30 * time.Second)
	fx.view.Click("刷新")
	if fx.codex.calls != 2 || fx.claude.calls != 3 {
		t.Fatalf("manual refresh at 61s: codex=%d claude=%d (want 2, 3)", fx.codex.calls, fx.claude.calls)
	}
	// The 429's Retry-After holds Codex past its own 60-second gap: receipt
	// + 90s, while Claude's floor is already over at +91s.
	*fx.now = fx.now.Add(30 * time.Second) // +91s
	fx.view.Click("刷新")
	if fx.codex.calls != 2 || fx.claude.calls != 4 {
		t.Fatalf("Retry-After must hold Codex: codex=%d claude=%d (want 2, 4)", fx.codex.calls, fx.claude.calls)
	}
	*fx.now = fx.now.Add(60 * time.Second) // +151s: past the 90s deadline
	fx.view.Click("刷新")
	if fx.codex.calls != 3 {
		t.Fatalf("Codex refetch after Retry-After expected, calls=%d", fx.codex.calls)
	}
}

// A refresh click the limits hold back says when the next look is due
// instead of doing nothing visible (PLAN.md 验收反馈).
func TestUsageRefreshClickSaysWhenHeldBack(t *testing.T) {
	fx := newUsageFixture(t, true)
	fx.codex.q = okQuota(aiusage.ToolCodex, "Plus", *fx.now)
	fx.claude.q = okQuota(aiusage.ToolClaude, "Pro", *fx.now)
	fx.view.Click("AI 用量")
	fx.f.tick(*fx.now)
	*fx.now = fx.now.Add(10 * time.Second)
	fx.view.Click("刷新")
	if fx.codex.calls != 1 || fx.claude.calls != 1 {
		t.Fatalf("inside the minimum gaps nothing may be asked: %d %d", fx.codex.calls, fx.claude.calls)
	}
	// Claude's 30-second floor is the binding one at this moment.
	if !strings.Contains(fx.a.status, "刚查过") || !strings.Contains(fx.a.status, "20 秒") {
		t.Fatalf("status = %q, want 刚查过，20 秒后可再刷新", fx.a.status)
	}
}

// The refresh status stays visible even when old data is already present.
func TestUsageRefreshClickShowsProgress(t *testing.T) {
	fx := newUsageFixture(t, true)
	fx.codex.q = okQuota(aiusage.ToolCodex, "Plus", *fx.now)
	fx.claude.q = okQuota(aiusage.ToolClaude, "Pro", *fx.now)
	fx.view.Click("AI 用量")
	fx.f.tick(*fx.now) // inline seams: both answered, data on screen
	flush := stubUsageSeams(t, true)
	*fx.now = fx.now.Add(5 * time.Minute)
	fx.view.Click("刷新")
	if fx.a.status != "正在刷新…" {
		t.Fatalf("a click must show progress, status=%q", fx.a.status)
	}
	flush()
	fx.f.tick(*fx.now)
	fx.view.Frame()
	if fx.view.HasText("正在更新…") {
		t.Fatal("finished work must settle back to the data stamp")
	}
	if !fx.view.HasText("刚刚更新") {
		t.Fatalf("the back row lost the data stamp after the refresh; texts %v", fx.view.Texts())
	}
}

func TestUsageRetryAfterRespected(t *testing.T) {
	fx := newUsageFixture(t, true)
	fx.codex.q = aiusage.Quota{
		Tool: aiusage.ToolCodex, Status: aiusage.QuotaRateLimited,
		FetchedAt: *fx.now, RetryAfter: 10 * time.Minute,
	}
	fx.claude.q = okQuota(aiusage.ToolClaude, "Pro", *fx.now)
	fx.view.Click("AI 用量")
	fx.f.tick(*fx.now)
	if fx.codex.calls != 1 {
		t.Fatalf("first fetch expected, got %d", fx.codex.calls)
	}
	// Both the 5-minute interval and the refresh button must yield to
	// the server's RetryAfter.
	fx.f.tick(fx.now.Add(5 * time.Minute))
	fx.f.requestRefresh()
	if fx.codex.calls != 1 {
		t.Fatalf("RetryAfter was not respected, calls=%d", fx.codex.calls)
	}
	if fx.claude.calls != 2 {
		t.Fatalf("the other tool still refetches on its own schedule, calls=%d", fx.claude.calls)
	}
	fx.f.tick(fx.now.Add(10 * time.Minute))
	if fx.codex.calls != 2 {
		t.Fatalf("fetch allowed after RetryAfter, calls=%d", fx.codex.calls)
	}
}

func TestUsageQuotaStates(t *testing.T) {
	cases := []struct {
		name   string
		status aiusage.QuotaStatus
		want   string
	}{
		{"not-logged-in", aiusage.QuotaNotLoggedIn, "未登录 Codex"},
		{"expired", aiusage.QuotaExpired, "登录已过期"},
		{"rate-limited", aiusage.QuotaRateLimited, "额度查询暂时被限流"},
		{"failed", aiusage.QuotaFailed, "暂时查不到额度"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fx := newUsageFixture(t, true)
			fx.codex.q = aiusage.Quota{Tool: aiusage.ToolCodex, Status: tc.status, FetchedAt: *fx.now}
			fx.claude.q = okQuota(aiusage.ToolClaude, "Pro", *fx.now)
			fx.view.Click("AI 用量")
			fx.f.tick(*fx.now)
			fx.view.Frame()
			if !fx.view.HasText(tc.want) {
				t.Fatalf("missing %q; texts=%v", tc.want, fx.view.Texts())
			}
		})
	}
}

// A stale card keeps the last good numbers (dimmed) when the newest fetch
// failed, and the module bar adds 「· 网络不通」.
func TestUsageStaleDataWithOfflineMarker(t *testing.T) {
	fx := newUsageFixture(t, true)
	fx.codex.qs = []aiusage.Quota{
		okQuota(aiusage.ToolCodex, "Plus", *fx.now),
		{Tool: aiusage.ToolCodex, Status: aiusage.QuotaFailed, FetchedAt: fx.now.Add(5 * time.Minute)},
	}
	fx.claude.q = okQuota(aiusage.ToolClaude, "Pro", *fx.now)
	fx.view.Click("AI 用量")
	fx.f.tick(*fx.now)
	*fx.now = fx.now.Add(5 * time.Minute)
	fx.f.tick(*fx.now)
	fx.view.Frame()
	for _, want := range []string{"5%", "网络不通", "5 分钟前的数据"} {
		if !fx.view.HasText(want) {
			t.Fatalf("stale view missing %q; texts=%v", want, fx.view.Texts())
		}
	}
}

func TestUsageLowRemainingWarns(t *testing.T) {
	fx := newUsageFixture(t, true)
	q := okQuota(aiusage.ToolClaude, "Pro", *fx.now)
	q = windowUsed(q, 0, 94) // 6% left → 快用完了
	fx.claude.q = q
	fx.codex.q = okQuota(aiusage.ToolCodex, "Plus", *fx.now)
	fx.view.Click("AI 用量")
	fx.f.tick(*fx.now)
	fx.view.Frame()
	if !fx.view.HasText("快用完了") {
		t.Fatalf("low remaining must warn; texts=%v", fx.view.Texts())
	}
}

func TestUsagePaceRunsOut(t *testing.T) {
	fx := newUsageFixture(t, true)
	fx.codex.q = okQuota(aiusage.ToolCodex, "Plus", *fx.now)
	fx.claude.q = okQuota(aiusage.ToolClaude, "Pro", *fx.now)
	fx.f.deps.pace = func(w aiusage.Window, now time.Time) aiusage.PaceInfo {
		return aiusage.PaceInfo{ExpectedLeftPct: 60, AheadPct: 17, RunsOutIn: time.Hour + 42*time.Minute, LastsUntilReset: false}
	}
	fx.view.Click("AI 用量")
	fx.f.tick(*fx.now)
	fx.view.Frame()
	if !fx.view.HasText("5 小时 · 1:42 后用完") {
		t.Fatalf("depletion line missing; texts=%v", fx.view.Texts())
	}
}

func TestUsageRangeSwitchRequestsNewSummary(t *testing.T) {
	fx := newUsageFixture(t, true)
	fx.codex.q = okQuota(aiusage.ToolCodex, "Plus", *fx.now)
	fx.claude.q = okQuota(aiusage.ToolClaude, "Pro", *fx.now)
	fx.view.Click("AI 用量")
	fx.f.tick(*fx.now)
	fx.view.Frame()
	if got := fx.scan.ranges; len(got) != 1 || got[0] != aiusage.RangeToday {
		t.Fatalf("first summary must be 今天, got %v", got)
	}
	if err := fx.view.Click("7 天"); err != nil {
		t.Fatalf("range segment: %v", err)
	}
	fx.view.Frame()
	if got := fx.scan.ranges; len(got) < 2 || got[len(got)-1] != aiusage.Range7d {
		t.Fatalf("switching must summarise 7 天, ranges=%v", got)
	}
}

// The first pass over local records shows the progress card while the
// scan is in flight.
func TestUsageFirstScanProgress(t *testing.T) {
	fx := newUsageFixture(t, true)
	// Re-stub goWork queued: the view can render while the scan is out.
	flush := func() {}
	{
		origWork, origMain := goWork, dispatchMain
		var queue []func()
		goWork = func(fn func()) { queue = append(queue, fn) }
		dispatchMain = func(fn func()) { queue = append(queue, fn) }
		flush = func() {
			for len(queue) > 0 {
				fn := queue[0]
				queue = queue[1:]
				fn()
			}
		}
		t.Cleanup(func() { goWork, dispatchMain = origWork, origMain })
	}
	fx.view.Click("AI 用量")
	fx.f.tick(*fx.now)
	fx.view.Frame()
	if !fx.view.HasText("第一次统计本机记录") {
		t.Fatalf("progress card missing; texts=%v", fx.view.Texts())
	}
	flush()
	fx.view.Frame()
	if fx.view.HasText("第一次统计本机记录") {
		t.Fatal("progress card must leave once the summary is in")
	}
}

// Numbers on the KPI row follow the compact formats: ≤3 significant digits,
// a decimal only under 100 of the unit (164M / 4.7M / 626 / ≈$+ / %), and
// the token split must fit its cell at real-world magnitudes.
func TestUsageKpiFormats(t *testing.T) {
	fx := newUsageFixture(t, true)
	fx.codex.q = okQuota(aiusage.ToolCodex, "Plus", *fx.now)
	fx.claude.q = okQuota(aiusage.ToolClaude, "Pro", *fx.now)
	now := *fx.now
	fx.scan.summaries = map[aiusage.Range]aiusage.Summary{
		aiusage.RangeToday: {
			Range: aiusage.RangeToday,
			Total: aiusage.Totals{Input: 2_500_000, Output: 467_000, CacheRead: 161_000_000, Requests: 626, Cost: 42.96, Unpriced: 143},
			ByTool: map[aiusage.Tool]aiusage.Totals{
				aiusage.ToolOmp:    {Input: 400_000, Output: 67_000, CacheRead: 119_933_000, Requests: 600, Cost: 42.96},
				aiusage.ToolClaude: {Input: 2_000_000, Output: 300_000, CacheRead: 1_000_000, Requests: 20},
				aiusage.ToolCodex:  {Output: 100_000, CacheRead: 40_000_000, Requests: 6, Unpriced: 143},
			},
			Buckets: []aiusage.Bucket{{Start: now.Add(-time.Hour), Tokens: map[aiusage.Tool]int64{aiusage.ToolOmp: 120_000_000, aiusage.ToolCodex: 40_000_000, aiusage.ToolClaude: 3_200_000}}},
		},
	}
	fx.view.Click("AI 用量")
	fx.f.tick(*fx.now)
	fx.view.Frame()
	for _, want := range []string{"164M", "626", "≈$42.96+", "98%"} {
		if !fx.view.HasText(want) {
			t.Fatalf("KPI %q missing; texts=%v", want, fx.view.Texts())
		}
	}
}

// usageTokens caps at 3 significant digits: one decimal under 100 of the
// unit, none at ≥100 — the shapes that appear in real session logs.
func TestUsageTokensFormat(t *testing.T) {
	for in, want := range map[int64]string{
		0:             "0",
		999:           "999",
		1_500:         "1.5K",
		39_300:        "39.3K",
		100_000:       "100K",
		467_000:       "467K",
		1_300_000:     "1.3M",
		4_700_000:     "4.7M",
		98_940_000:    "98.9M",
		99_960_000:    "100M",
		290_628_159:   "291M",
		2_217_105_299: "2.2B",
	} {
		if got := usageTokens(in); got != want {
			t.Errorf("usageTokens(%d) = %q, want %q", in, got, want)
		}
	}
}

// Per-source settings hide their card or the whole local section.
func TestUsagePerSourceSettings(t *testing.T) {
	fx := newUsageFixture(t, true)
	fx.codex.q = okQuota(aiusage.ToolCodex, "Plus", *fx.now)
	fx.claude.q = okQuota(aiusage.ToolClaude, "Pro", *fx.now)
	fx.a.settings.AIUsage.QuotaCodex = false
	fx.view.Click("AI 用量")
	fx.f.tick(*fx.now)
	fx.view.Frame()
	if fx.codex.calls != 0 {
		t.Fatal("disabled quota source must not be fetched")
	}
	if fx.claude.calls != 1 {
		t.Fatal("the enabled source still fetches")
	}
	if !fx.view.HasText("Claude Code") {
		t.Fatal("enabled card missing")
	}
	// With every local source off the 本机用量 section leaves.
	fx.a.settings.AIUsage.LocalOmp = false
	fx.a.settings.AIUsage.LocalClaude = false
	fx.a.settings.AIUsage.LocalCodex = false
	fx.view.Frame()
	if fx.view.HasText("本机用量") {
		t.Fatal("local section must hide when every source is off")
	}
}

// The settings group writes through applySettings: the running copy and
// settings.json change together. The layout is design/usage-fix-v2.png's
// table: one-line master switch, a header row naming the two columns,
// one row per tool with a checkbox per column (omp's quota cell is a
// plain 「—」), and the privacy note under the card.
func TestUsageSettingsSection(t *testing.T) {
	stubSettingsSeams(t, false, nil, nil)
	a, view := newSettingsShelf(t)
	openSettingsPage(t, view, "AI 用量")
	for _, want := range []string{"AI 用量", "在刘海里显示 AI 用量", "套餐额度", "本机用量", "Codex", "Claude Code", "omp", "—", "额度显示", "已用", "剩余", "不发给其他任何地方"} {
		if !view.HasText(want) {
			t.Fatalf("AI 用量 group missing %q; texts=%v", want, view.Texts())
		}
	}
	clickVisible(t, view, "在刘海里显示 AI 用量")
	if !a.settings.AIUsage.Enabled {
		t.Fatal("the master switch did not apply")
	}
	loaded, err := settings.Load(a.dataDir)
	if err != nil || !loaded.AIUsage.Enabled {
		t.Fatalf("settings.json lacks the change: %v %+v", err, loaded.AIUsage)
	}
	clickVisible(t, view, "套餐额度 Claude Code")
	if a.settings.AIUsage.QuotaClaude {
		t.Fatal("the Claude quota checkbox did not turn off")
	}
	clickVisible(t, view, "本机用量 omp")
	if a.settings.AIUsage.LocalOmp {
		t.Fatal("the omp local checkbox did not turn off")
	}
	// The display mode is a saved preference: used by default, left after
	// one click.
	if a.settings.AIUsage.QuotaLeft {
		t.Fatal("used must be the default")
	}
	clickVisible(t, view, "剩余")
	if !a.settings.AIUsage.QuotaLeft {
		t.Fatal("the 剩余 segment did not apply")
	}
	if saved, err := settings.Load(a.dataDir); err != nil || !saved.AIUsage.QuotaLeft {
		t.Fatalf("settings.json lacks quotaLeft: %v %+v", err, saved.AIUsage)
	}
}

// With the master off the source table is locked: clicks reach the boxes
// but flip nothing, while the saved checkbox states stay as they were —
// the mock's dimmed, non-interactive state.
func TestUsageSettingsTableLockedWhileOff(t *testing.T) {
	stubSettingsSeams(t, false, nil, nil)
	a, view := newSettingsShelf(t) // AIUsage.Enabled defaults to false
	openSettingsPage(t, view, "AI 用量")
	before := a.settings.AIUsage
	clickVisible(t, view, "套餐额度 Codex")
	clickVisible(t, view, "本机用量 Claude Code")
	if a.settings.AIUsage != before {
		t.Fatalf("locked table answered clicks: %+v", a.settings.AIUsage)
	}
	// The master still flips while the table is locked.
	clickVisible(t, view, "在刘海里显示 AI 用量")
	if !a.settings.AIUsage.Enabled {
		t.Fatal("master switch must work while the table is locked")
	}
	// With the table unlocked the checkboxes apply again: omp's local
	// checkbox flips to off and the change is saved.
	clickVisible(t, view, "本机用量 omp")
	if a.settings.AIUsage.LocalOmp {
		t.Fatal("unlocked table must answer checkbox clicks")
	}
	saved, err := settings.Load(a.dataDir)
	if err != nil || !saved.AIUsage.Enabled || saved.AIUsage.LocalOmp {
		t.Fatalf("saved state wrong: %v %+v", err, saved.AIUsage)
	}
}

// The card's logo sits on the ring's centre: render one quotaRing, find
// the logo's ink pixels by colour and check their centroid is the circle
// centre — (25, 25) inside the 50pt box at the view's origin.
func TestQuotaRingLogoCentred(t *testing.T) {
	for _, tc := range []struct {
		name string
		tool aiusage.Tool
		// match reports whether an 8-bit pixel is logo ink
		match func(r, g, b uint32) bool
	}{
		{"codex", aiusage.ToolCodex, func(r, g, b uint32) bool {
			// white Blossom ink, under the #FFF of the pace ticks
			return r+g+b > 640 && min(min(r, g), b) < 253
		}},
		{"claude", aiusage.ToolClaude, func(r, g, b uint32) bool {
			// #D97757 spark ink: g<140 and b>50 keep the #FF9F0A ring
			// (g=159, b=10) and the white ticks out.
			return r > 170 && g > 80 && g < 140 && b > 50 && b < 130
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fx := newUsageFixture(t, true)
			now := *fx.now
			view := ui.NewTester(func(c *ui.Context) {
				fx.f.quotaRing(c, tc.tool, okQuota(tc.tool, "Plus", now), false)
			}, usageRingSize, usageRingSize)
			view.Frame()
			img := view.Image()
			b := img.Bounds()
			var sx, sy, n float64
			// Only the middle square: the ring's arcs and pace ticks change
			// with the used/left setting, so counting them would test the
			// meter, not the logo.
			const mid = 9
			for y := b.Min.Y + usageRingSize/2 - mid; y < b.Min.Y+usageRingSize/2+mid; y++ {
				for x := b.Min.X + usageRingSize/2 - mid; x < b.Min.X+usageRingSize/2+mid; x++ {
					r, g, bl, _ := img.At(x, y).RGBA()
					r, g, bl = r>>8, g>>8, bl>>8
					if tc.match(r, g, bl) {
						sx += float64(x - b.Min.X)
						sy += float64(y - b.Min.Y)
						n++
					}
				}
			}
			if n == 0 {
				t.Fatalf("no %s logo pixels found", tc.name)
			}
			cx, cy := sx/n, sy/n
			if math.Abs(cx-usageRingSize/2) > 1.5 || math.Abs(cy-usageRingSize/2) > 1.5 {
				t.Fatalf("%s logo centroid (%.1f, %.1f) is not the ring centre (25, 25); %d ink px",
					tc.name, cx, cy, int(n))
			}
		})
	}
}

// A disabled feature must never appear on the ear even though it is
// registered; the current page falls back to the tray.
func TestUsageDisabledFallsBackToTray(t *testing.T) {
	fx := newUsageFixture(t, false)
	fx.a.tab = pageUsage
	fx.view.Frame()
	if got := fx.a.currentPage().id(); got != pageTray {
		t.Fatalf("current page must fall back to tray, got %s", got)
	}
}

// Disabling the feature, hiding the panel, or leaving the page cancels
// in-flight work at once — on the transition itself, not the next tick:
// queued workers see a cancelled context and their results are dropped
// instead of written into the page state.
func TestUsageHidingCancelsWork(t *testing.T) {
	fx := newUsageFixture(t, true)
	flush := stubUsageSeams(t, true) // override the fixture's inline stubs
	fx.codex.q = okQuota(aiusage.ToolCodex, "Plus", *fx.now)
	fx.claude.q = okQuota(aiusage.ToolClaude, "Pro", *fx.now)
	fx.view.Click("AI 用量")
	fx.f.tick(*fx.now)
	if !fx.f.localBusy && !fx.f.quota[0].busy && !fx.f.quota[1].busy {
		t.Fatal("expected work in flight")
	}

	// hide() itself retires everything — no tick in between: the queued
	// workers then run with a cancelled context and are dropped.
	fx.a.hide(hideExplicit)
	if fx.f.localBusy || fx.f.quota[0].busy || fx.f.quota[1].busy {
		t.Fatal("busy flags must clear synchronously on hide")
	}
	flush()
	if fx.codex.calls != 1 || fx.claude.calls != 1 || fx.scan.scans != 1 {
		t.Fatalf("queued work still ran: %d %d %d", fx.codex.calls, fx.claude.calls, fx.scan.scans)
	}
	if fx.codex.cancelledAt != 1 || fx.claude.cancelledAt != 1 || !fx.scan.cancelled {
		t.Fatal("workers must see a cancelled context")
	}
	if !fx.f.quota[0].latest.FetchedAt.IsZero() || fx.f.hasSummary {
		t.Fatal("retired results must not publish")
	}

	// Coming back starts a fresh generation that publishes again.
	fx.a.visible = true
	fx.a.setTab(pageUsage)
	fx.f.tick(*fx.now)
	flush()
	if !fx.f.hasSummary || fx.f.quota[0].latest.Status != aiusage.QuotaOK {
		t.Fatal("the next visibility pass must publish normally")
	}
}

// Leaving the usage page cancels work the same way, synchronously in
// setTab — no tick needed.
func TestUsageLeavingPageCancelsWork(t *testing.T) {
	fx := newUsageFixture(t, true)
	flush := stubUsageSeams(t, true)
	fx.view.Click("AI 用量")
	fx.f.tick(*fx.now)

	fx.a.setTab(pageTray)
	if fx.f.localBusy || fx.f.quota[0].busy || fx.f.quota[1].busy {
		t.Fatal("busy flags must clear synchronously on page departure")
	}
	flush()
	if !fx.scan.cancelled || fx.codex.cancelledAt == 0 || fx.claude.cancelledAt == 0 {
		t.Fatal("leaving the page must cancel in-flight work")
	}
	if fx.f.hasSummary || !fx.f.quota[0].latest.FetchedAt.IsZero() {
		t.Fatal("results landing after the switch must be dropped")
	}
}

// The clipboard-search hotkey jumps straight to pageClipboard — through
// the same switchTab openClipboardSearch uses — and must retire the usage
// page's work just like a setTab departure, synchronously.
func TestUsageSearchShortcutStopsWork(t *testing.T) {
	fx := newUsageFixture(t, true)
	flush := stubUsageSeams(t, true)
	fx.view.Click("AI 用量")
	fx.f.tick(*fx.now)

	fx.a.switchTab(pageClipboard) // what openClipboardSearch assigns
	if fx.a.tab != pageClipboard {
		t.Fatal("switchTab must land on the clipboard page")
	}
	if fx.f.localBusy || fx.f.quota[0].busy || fx.f.quota[1].busy {
		t.Fatal("busy flags must clear synchronously on the shortcut jump")
	}
	flush()
	if !fx.scan.cancelled || fx.codex.cancelledAt == 0 || fx.claude.cancelledAt == 0 {
		t.Fatal("the search shortcut must cancel in-flight usage work")
	}
}

// Switching the feature off mid-flight retires work the same way: the
// setter itself cancels, before any tick could notice.
func TestUsageDisableCancelsWork(t *testing.T) {
	fx := newUsageFixture(t, true)
	flush := stubUsageSeams(t, true) // override the fixture's inline stubs
	fx.view.Click("AI 用量")
	fx.f.tick(*fx.now)
	fx.a.setAIUsage(func(s *settings.Settings) { s.AIUsage.Enabled = false })
	if fx.f.localBusy || fx.f.quota[0].busy || fx.f.quota[1].busy {
		t.Fatal("busy flags must clear synchronously on disable")
	}
	flush()
	if !fx.scan.cancelled || fx.codex.cancelledAt == 0 || fx.claude.cancelledAt == 0 {
		t.Fatal("disable must cancel in-flight work")
	}
}

// Hiding while a fetch is in flight cancels it; a cancelled attempt must
// allow a new Codex fetch, but an unasked Claude read still waits for its
// random 5–15 minute gap.
func TestUsageReopenRefetchesAfterCancelledAttempt(t *testing.T) {
	fx := newUsageFixture(t, true)
	flush := stubUsageSeams(t, true)
	fx.codex.q = aiusage.Quota{Status: aiusage.QuotaFailed}
	fx.claude.q = aiusage.Quota{Status: aiusage.QuotaFailed}
	fx.view.Click("AI 用量")
	fx.f.tick(*fx.now)

	fx.a.hide(hideExplicit)
	flush()
	if !fx.f.quota[0].attemptAt.IsZero() {
		t.Fatal("cancelled Codex work must allow an immediate retry")
	}
	if fx.f.quota[1].attemptAt.IsZero() {
		t.Fatal("cancelled Claude work must keep its attempt time")
	}
	if fx.f.quota[1].latest.FetchedAt.IsZero() {
		t.Fatal("Claude must show its result instead of remaining on 正在查询…")
	}
	fx.codex.q = okQuota(aiusage.ToolCodex, "Plus", *fx.now)

	*fx.now = fx.now.Add(2 * time.Minute) // before Claude's 5-minute gap
	fx.a.visible = true
	fx.a.setTab(pageUsage)
	fx.f.tick(*fx.now)
	flush()
	if fx.codex.calls != 2 || fx.claude.calls != 1 {
		t.Fatalf("reopen must retry Codex but not an unasked Claude read: %d %d", fx.codex.calls, fx.claude.calls)
	}
	fx.view.Frame()
	if fx.view.HasText("正在查询…") {
		t.Fatal("the refetched answer must replace 正在查询…")
	}
	if !fx.view.HasText("5%") {
		t.Fatalf("the card must show the new numbers; texts=%v", fx.view.Texts())
	}
}

// A cancelled refresh keeps the previous answer: hiding mid-refresh must
// not turn shown quota into QuotaFailed/「网络不通」 (acceptance review).
func TestUsageCancelledRefreshKeepsPriorAnswer(t *testing.T) {
	fx := newUsageFixture(t, true)
	flush := stubUsageSeams(t, true)
	fx.codex.q = okQuota(aiusage.ToolCodex, "Plus", *fx.now)
	fx.claude.q = okQuota(aiusage.ToolClaude, "Pro", *fx.now)
	fx.view.Click("AI 用量")
	fx.f.tick(*fx.now)
	flush()
	good := fx.f.quota[1].latest
	if good.Status != aiusage.QuotaOK {
		t.Fatal("first look must publish the good answer")
	}

	*fx.now = fx.now.Add(6 * time.Minute) // past both manual refresh floors
	fx.f.requestRefresh()
	if !fx.f.quota[1].busy {
		t.Fatal("the manual refresh must start a Claude fetch")
	}

	fx.a.hide(hideExplicit) // cancel in flight; the fetch dies with ctx
	flush()
	if got := fx.f.quota[1].latest; got.FetchedAt != good.FetchedAt || len(got.Windows) != len(good.Windows) {
		t.Fatalf("cancelled refresh must keep the previous answer, got %+v", got)
	}
	if fx.f.offline() {
		t.Fatal("a cancelled refresh must not report 网络不通")
	}

	fx.a.visible = true
	fx.a.setTab(pageUsage)
	fx.f.tick(*fx.now)
	flush()
	fx.view.Frame()
	if !fx.view.HasText("5%") {
		t.Fatalf("reopening must still show the kept answer; texts=%v", fx.view.Texts())
	}
	if fx.claude.calls != 2 {
		t.Fatalf("the floor holds: calls=%d, want 2", fx.claude.calls)
	}
}

// A fetch that answered before the panel hid still publishes: the page
// shows the real result when it comes back instead of a stale
// 「正在查询…」, and the interval keeps the endpoint from being asked again.
func TestUsageRetiredAnswerStillPublishes(t *testing.T) {
	fx := newUsageFixture(t, true)
	var work, mainQ []func()
	origWork, origMain := goWork, dispatchMain
	goWork = func(fn func()) { work = append(work, fn) }
	dispatchMain = func(fn func()) { mainQ = append(mainQ, fn) }
	t.Cleanup(func() { goWork, dispatchMain = origWork, origMain })

	fx.codex.q = okQuota(aiusage.ToolCodex, "Plus", *fx.now)
	fx.claude.q = okQuota(aiusage.ToolClaude, "Pro", *fx.now)
	fx.view.Click("AI 用量")
	fx.f.tick(*fx.now)
	for _, fn := range work { // the fetches answer, the dispatches queue
		fn()
	}
	work = nil
	fx.a.hide(hideExplicit) // retires the generation before the answers land
	for _, fn := range mainQ {
		fn()
	}
	mainQ = nil
	for _, tool := range quotaTools() {
		st := &fx.f.quota[quotaIndex(tool)]
		if st.latest.FetchedAt.IsZero() {
			t.Fatalf("%v: an answered fetch must publish even when retired", tool)
		}
		if st.busy {
			t.Fatalf("%v: a retired answer must not leave the card busy", tool)
		}
	}

	*fx.now = fx.now.Add(2 * time.Minute)
	fx.a.visible = true
	fx.a.setTab(pageUsage)
	fx.f.tick(*fx.now)
	if fx.codex.calls != 1 || fx.claude.calls != 1 {
		t.Fatalf("a published answer must keep the interval: %d %d", fx.codex.calls, fx.claude.calls)
	}
	fx.view.Frame()
	if fx.view.HasText("正在查询…") {
		t.Fatal("reopened card shows 正在查询… although the fetch answered")
	}
	if !fx.view.HasText("5%") {
		t.Fatalf("reopened card must show the recorded numbers; texts=%v", fx.view.Texts())
	}
}

// A late 429 must still back the endpoint off after the panel hides.
func TestUsageLate429KeepsBackoff(t *testing.T) {
	fx := newUsageFixture(t, true)
	flush := stubUsageSeams(t, true)
	fx.claude.q = aiusage.Quota{
		Tool: aiusage.ToolClaude, Status: aiusage.QuotaRateLimited,
		FetchedAt: *fx.now, RetryAfter: 5 * time.Minute,
	}
	fx.claude.answerAfterCancel = true
	fx.view.Click("AI 用量")
	fx.f.tick(*fx.now)
	fx.a.hide(hideExplicit)
	flush() // the cancelled fetch's 429 lands, late

	st := &fx.f.quota[quotaIndex(aiusage.ToolClaude)]
	want := fx.now.Add(5 * time.Minute)
	if !st.retryTill.Equal(want) {
		t.Fatalf("retryTill = %v, want %v", st.retryTill, want)
	}

	// Reopen before the backoff ends: no second request.
	*fx.now = fx.now.Add(3 * time.Minute)
	fx.a.visible = true
	fx.a.setTab(pageUsage)
	fx.f.tick(*fx.now)
	flush()
	if fx.claude.calls != 1 {
		t.Fatalf("RetryAfter was lost with the discarded 429: calls=%d", fx.claude.calls)
	}
}

// The depletion line shows the earliest runout across both windows, not
// whichever the loop visited last.
func TestUsagePaceEarliestDepletion(t *testing.T) {
	fx := newUsageFixture(t, true)
	fx.codex.q = okQuota(aiusage.ToolCodex, "Plus", *fx.now)
	fx.claude.q = okQuota(aiusage.ToolClaude, "Pro", *fx.now)
	fx.f.deps.pace = func(w aiusage.Window, now time.Time) aiusage.PaceInfo {
		if w.Length == 5*time.Hour {
			return aiusage.PaceInfo{ExpectedLeftPct: 60, AheadPct: 20, RunsOutIn: 42 * time.Minute, LastsUntilReset: false}
		}
		return aiusage.PaceInfo{ExpectedLeftPct: 60, AheadPct: 20, RunsOutIn: 30 * time.Hour, LastsUntilReset: false}
	}
	fx.view.Click("AI 用量")
	fx.f.tick(*fx.now)
	fx.view.Frame()
	if !fx.view.HasText("5 小时 · 0:42 后用完") {
		t.Fatalf("earliest depletion expected; texts=%v", fx.view.Texts())
	}
}

// Under a quarter remaining the ring must draw that fraction, not always a
// quarter-circle: paint where the arc should reach, nothing where it must
// not.
func TestUsageRingArcSmallFraction(t *testing.T) {
	render := func(frac float32) *image.RGBA {
		view := ui.NewTester(func(c *ui.Context) {
			e := ui.Box(c).Size(64, 64)
			e.Draw(func(p *ui.Painter, r ui.Rect) {
				strokeRingArc(p, r.X+r.W/2, r.Y+r.H/2, 20, 5, frac, ui.RGB(255, 0, 0))
			})
		}, 64, 64)
		return view.Image()
	}
	painted := func(img *image.RGBA, x, y int) bool {
		r, g, b, _ := img.At(x, y).RGBA()
		r8, g8, b8 := r>>8, g>>8, b>>8
		return r8 > 150 && g8 < 80 && b8 < 80 // the red arc on the white canvas
	}
	cx, cy, rad := 32, 32, 20

	small := render(0.1) // 36° clockwise from 12 o'clock
	if !painted(small, cx, cy-rad) {
		t.Fatal("0.1 arc must start at 12 o'clock")
	}
	if painted(small, cx+rad, cy) {
		t.Fatal("0.1 arc must not reach 3 o'clock (was a fixed quarter)")
	}

	half := render(0.5) // 12 → 6 o'clock, the right half
	if !painted(half, cx+rad, cy) {
		t.Fatal("0.5 arc must reach 3 o'clock")
	}
	if !painted(half, cx, cy+rad) {
		t.Fatal("0.5 arc must reach 6 o'clock")
	}
	if painted(half, cx-rad, cy) {
		t.Fatal("0.5 arc must not reach 9 o'clock")
	}
}

// --- window lists, display mode, persistence, Claude cadence -------------

// The card states its share with the mode on the number, as Magpie's
// 「41% used」 does: used by default, left once the setting asks.
func TestUsageQuotaUsedByDefaultAndLeftSetting(t *testing.T) {
	fx := newUsageFixture(t, true)
	fx.codex.q = okQuota(aiusage.ToolCodex, "Plus", *fx.now)
	fx.claude.q = okQuota(aiusage.ToolClaude, "Pro", *fx.now)
	fx.view.Click("AI 用量")
	fx.f.tick(*fx.now)
	fx.view.Frame()
	for _, want := range []string{"5% 已用", "30% 已用"} {
		if !fx.view.HasText(want) {
			t.Fatalf("used reading missing %q; texts=%v", want, fx.view.Texts())
		}
	}
	fx.a.settings.AIUsage.QuotaLeft = true
	fx.view.Frame()
	for _, want := range []string{"95% 剩余", "70% 剩余"} {
		if !fx.view.HasText(want) {
			t.Fatalf("left reading missing %q; texts=%v", want, fx.view.Texts())
		}
	}
}

// Magpie's rounding: a whole number stays whole, anything else keeps one
// decimal.
func TestUsagePctText(t *testing.T) {
	for in, want := range map[float64]string{
		0:     "0%",
		41:    "41%",
		12.5:  "12.5%",
		12.34: "12.3%",
		99.96: "100.0%", // not a whole number, so one decimal, as Magpie
		-3:    "0%",
		120:   "100%",
	} {
		if got := quotaPctText(in); got != want {
			t.Errorf("quotaPctText(%v) = %q, want %q", in, got, want)
		}
	}
}

func TestUsageQuotaFractionalPercent(t *testing.T) {
	fx := newUsageFixture(t, true)
	q := okQuota(aiusage.ToolClaude, "Pro", *fx.now)
	q = windowUsed(q, 0, 12.5)
	q = windowUsed(q, 1, 41.04)
	fx.claude.q = q
	fx.codex.q = okQuota(aiusage.ToolCodex, "Plus", *fx.now)
	fx.view.Click("AI 用量")
	fx.f.tick(*fx.now)
	fx.view.Frame()
	for _, want := range []string{"12.5% 已用", "41.0% 已用"} {
		if !fx.view.HasText(want) {
			t.Fatalf("fractional reading missing %q; texts=%v", want, fx.view.Texts())
		}
	}
}

// A window at or above 90% used is red whichever share is shown.
func TestWindowColorMarksHeavyUse(t *testing.T) {
	week := aiusage.Window{Name: "7 days", UsedPct: 89.9, Length: 7 * 24 * time.Hour}
	if got := windowColor(week); got.R != usageWeekly.R || got.G != usageWeekly.G {
		t.Fatalf("89.9%% used = %v, want the weekly colour", got)
	}
	week.UsedPct = 90
	if got := windowColor(week); got.R != danger.R || got.G != danger.G {
		t.Fatalf("90%% used = %v, want danger", got)
	}
	five := aiusage.Window{Name: "5 hours", UsedPct: 95, Length: 5 * time.Hour}
	if got := windowColor(five); got.R != danger.R || got.G != danger.G {
		t.Fatalf("95%% used = %v, want danger", got)
	}
}

// A plan with per-model weekly windows keeps the two rolling ones on the
// card and lists every window in the hover detail.
func TestUsageScopedWindowsInHoverDetail(t *testing.T) {
	fx := newUsageFixture(t, true)
	q := okQuota(aiusage.ToolClaude, "Pro", *fx.now)
	q.Windows = append(q.Windows,
		aiusage.Window{Name: "7 days · Opus", Model: "opus", UsedPct: 37, Length: 7 * 24 * time.Hour},
		aiusage.Window{Name: "7 days · Sonnet", Model: "sonnet", UsedPct: 2, Length: 7 * 24 * time.Hour},
	)
	fx.claude.q = q
	fx.codex.q = okQuota(aiusage.ToolCodex, "Plus", *fx.now)
	fx.view.Click("AI 用量")
	fx.f.tick(*fx.now)
	fx.view.Frame()
	if fx.view.HasText("每周 · Opus") {
		t.Fatal("scoped windows do not fit the card; they belong to the hover detail")
	}
	// Hover Claude's card: the second 84pt card of the left column.
	fx.view.Move(usageLeftW/2, 2*usageCardH+usageCardGap+usageCardH/2)
	fx.view.Frame()
	fx.view.Frame()
	for _, want := range []string{"每周 · Opus", "37% 已用", "每周 · Sonnet", "2% 已用"} {
		if !fx.view.HasText(want) {
			t.Fatalf("hover detail missing %q; texts=%v", want, fx.view.Texts())
		}
	}
}

// Opening the page asks for a fresh Claude reading, but never faster than
// the 30-second floor.
func TestUsageClaudeAskFloor(t *testing.T) {
	fx := newUsageFixture(t, true)
	fx.claude.q = okQuota(aiusage.ToolClaude, "Pro", *fx.now)
	fx.codex.q = okQuota(aiusage.ToolCodex, "Plus", *fx.now)
	fx.view.Click("AI 用量")
	fx.f.tick(*fx.now)
	if fx.claude.calls != 1 {
		t.Fatalf("first visit must read once, calls=%d", fx.claude.calls)
	}
	// Reopening 10s later is an ask, but the floor holds it back.
	*fx.now = fx.now.Add(10 * time.Second)
	fx.a.hide(hideExplicit)
	fx.f.tick(*fx.now)
	fx.a.visible = true
	fx.a.setTab(pageUsage)
	fx.f.tick(*fx.now)
	if fx.claude.calls != 1 {
		t.Fatalf("a reopen inside 30s must not run /usage again, calls=%d", fx.claude.calls)
	}
	// Past the floor the ask runs at once, without waiting for the random
	// 5–15 minute gap. A tick while hidden marks the page as left, as the
	// monitor loop does every 30ms.
	*fx.now = fx.now.Add(21 * time.Second)
	fx.a.hide(hideExplicit)
	fx.f.tick(*fx.now)
	fx.a.visible = true
	fx.a.setTab(pageUsage)
	fx.f.tick(*fx.now)
	if fx.claude.calls != 2 {
		t.Fatalf("an ask after 30s must run /usage, calls=%d", fx.claude.calls)
	}
}

// An unasked Claude read happens only after the random gap AND only when
// Claude Code was used since the last attempt. The "was it used" answer is
// a background projects scan: one starts at most once a minute, and its
// answer reaches the scheduler on a later tick.
func TestUsageClaudeAutoReadNeedsUse(t *testing.T) {
	used := false
	fx := newUsageFixtureWith(t, true, func(d *usageDeps) {
		d.claudeUsedSince = func(time.Time) bool { return used }
	})
	fx.claude.q = okQuota(aiusage.ToolClaude, "Pro", *fx.now)
	fx.codex.q = okQuota(aiusage.ToolCodex, "Plus", *fx.now)
	fx.view.Click("AI 用量")
	fx.f.tick(*fx.now)
	if fx.claude.calls != 1 {
		t.Fatalf("first look calls=%d", fx.claude.calls)
	}
	// The gap elapsed; the first tick scans the projects tree, the answer
	// lands, and the following ticks read it.
	*fx.now = fx.now.Add(5 * time.Minute)
	fx.f.tick(*fx.now)
	fx.f.tick(*fx.now)
	if fx.claude.calls != 1 {
		t.Fatalf("an unused allowance must not be re-read, calls=%d", fx.claude.calls)
	}
	// Claude Code is used only after the last scan: a new scan is due once
	// the probe gap has passed, and its answer is read on the next tick.
	*fx.now = fx.now.Add(time.Minute)
	used = true
	fx.f.tick(*fx.now)
	if fx.claude.calls != 1 {
		t.Fatalf("the scan's answer must reach a later tick, calls=%d", fx.claude.calls)
	}
	fx.f.tick(*fx.now)
	if fx.claude.calls != 2 {
		t.Fatalf("a used allowance must be re-read when due, calls=%d", fx.claude.calls)
	}
	*fx.now = fx.now.Add(4 * time.Minute)
	fx.f.tick(*fx.now)
	fx.f.tick(*fx.now)
	if fx.claude.calls != 2 {
		t.Fatalf("the next random gap is not over yet, calls=%d", fx.claude.calls)
	}
	*fx.now = fx.now.Add(time.Minute)
	fx.f.tick(*fx.now)
	fx.f.tick(*fx.now)
	fx.f.tick(*fx.now)
	if fx.claude.calls != 3 {
		t.Fatalf("the next gap elapsed, calls=%d", fx.claude.calls)
	}
}

// The automatic gap is a whole minute from 5 to 15.
func TestDefaultClaudeWaitRange(t *testing.T) {
	seen := map[time.Duration]bool{}
	for range 200 {
		d := defaultClaudeWait()
		if d%time.Minute != 0 || d < 5*time.Minute || d > 15*time.Minute {
			t.Fatalf("wait = %v, want a whole 5–15 minutes", d)
		}
		seen[d] = true
	}
	if len(seen) < 2 {
		t.Fatalf("the wait never varies: %v", seen)
	}
}

// The last good reading loads before the first fetch of a session, so a
// restart shows numbers instead of a blank card.
func TestUsageLoadsSavedQuota(t *testing.T) {
	saved := okQuota(aiusage.ToolClaude, "Pro", fakeSavedAt)
	fx := newUsageFixtureWith(t, true, func(d *usageDeps) {
		d.loadLast = func() []aiusage.Quota { return []aiusage.Quota{saved} }
	})
	fx.a.tab, fx.a.level = pageUsage, 2
	fx.view.Frame()
	for _, want := range []string{"Claude Code", "5% 已用", "10 分钟前更新"} {
		if !fx.view.HasText(want) {
			t.Fatalf("saved reading missing %q; texts=%v", want, fx.view.Texts())
		}
	}
	if fx.claude.calls != 0 {
		t.Fatal("drawing the saved reading must not fetch")
	}
}

var fakeSavedAt = time.Date(2026, 10, 19, 11, 50, 0, 0, time.Local)

// A good answer is kept for the next start; a failed one never replaces it.
func TestUsageSavesGoodQuota(t *testing.T) {
	var saved []aiusage.Quota
	fx := newUsageFixtureWith(t, true, func(d *usageDeps) {
		d.saveLast = func(q aiusage.Quota) { saved = append(saved, q) }
	})
	fx.codex.q = okQuota(aiusage.ToolCodex, "Plus", *fx.now)
	fx.claude.q = okQuota(aiusage.ToolClaude, "Pro", *fx.now)
	fx.view.Click("AI 用量")
	fx.f.tick(*fx.now)
	tools := map[aiusage.Tool]bool{}
	for _, q := range saved {
		if q.Status == aiusage.QuotaOK && len(q.Windows) > 0 {
			tools[q.Tool] = true
		}
	}
	if !tools[aiusage.ToolCodex] || !tools[aiusage.ToolClaude] {
		t.Fatalf("saved = %+v, want both tools' good readings", saved)
	}
	fx.codex.q = aiusage.Quota{Tool: aiusage.ToolCodex, Status: aiusage.QuotaFailed, FetchedAt: *fx.now}
	*fx.now = fx.now.Add(5 * time.Minute)
	fx.f.tick(*fx.now)
	for _, q := range saved {
		if q.Status != aiusage.QuotaOK || len(q.Windows) == 0 {
			t.Fatalf("a failed answer was saved: %+v", q)
		}
	}
}

// A machine without Claude Code says so instead of pretending the account
// is signed out or the quota is 0%.
func TestUsageNoClaudeCodeMessage(t *testing.T) {
	fx := newUsageFixture(t, true)
	fx.codex.q = okQuota(aiusage.ToolCodex, "Plus", *fx.now)
	fx.claude.q = aiusage.Quota{Tool: aiusage.ToolClaude, Status: aiusage.QuotaNoCLI, FetchedAt: *fx.now}
	fx.view.Click("AI 用量")
	fx.f.tick(*fx.now)
	fx.view.Frame()
	if !fx.view.HasText("未找到 Claude Code") {
		t.Fatalf("missing the no-CLI message; texts=%v", fx.view.Texts())
	}
}

// --- screenshots -------------------------------------------------------------

// usageShotShelf is a visible shelf on the usage page with fake deps and a
// fixed clock, for the screenshot tests and any render-level check.
type usageShotShelf struct {
	a      *shelf
	f      *usageFeature
	codex  *fakeQuotaFetch
	claude *fakeQuotaFetch
	scan   *fakeUsageScanner
}

func newUsageShot(now time.Time) *usageShotShelf {
	a := &shelf{dataDir: os.TempDir(), visible: true, settings: settings.Default(),
		topInset: 32, notchW: 180, notchH: 32, hasNotch: true,
		thumbnails: map[string]*ui.Bitmap{}, thumbPending: map[string]bool{}, appNames: map[string]string{}}
	a.settings.AIUsage.Enabled = true
	s := &usageShotShelf{a: a, codex: &fakeQuotaFetch{}, claude: &fakeQuotaFetch{}}
	s.scan = &fakeUsageScanner{summaries: usageShotSummaries(now)}
	s.f = newUsageFeature(a, usageDeps{
		fetchCodex:  s.codex.fetch,
		fetchClaude: s.claude.fetch,
		pace: func(w aiusage.Window, now time.Time) aiusage.PaceInfo {
			return aiusage.PaceInfo{ExpectedLeftPct: 60, AheadPct: 5, LastsUntilReset: true}
		},
		newScanner: func() usageScanner { return s.scan },
		now:        func() time.Time { return now },
	})
	a.registerPage(s.f)
	a.tab = pageUsage
	return s
}

// usageShotSummaries is the mock's 今天 page: the same 164M / 626 / ≈$42.96
// numbers and the 24 hourly bars.
func usageShotSummaries(now time.Time) map[aiusage.Range]aiusage.Summary {
	buckets := make([]aiusage.Bucket, 24)
	hours := []int64{38, 36, 19, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 32, 34, 2, 0, 0, 0, 0, 0, 0, 0, 0}
	for i, v := range hours {
		buckets[i] = aiusage.Bucket{Start: now.Add(time.Duration(i-24) * time.Hour), Tokens: map[aiusage.Tool]int64{
			aiusage.ToolOmp:    v * 1_000_000 * 72 / 100,
			aiusage.ToolClaude: v * 1_000_000 * 25 / 100,
			aiusage.ToolCodex:  v * 1_000_000 * 3 / 100,
		}}
	}
	return map[aiusage.Range]aiusage.Summary{
		aiusage.RangeToday: {
			Range: aiusage.RangeToday,
			Total: aiusage.Totals{Input: 2_500_000, Output: 467_000, CacheRead: 161_000_000, Requests: 626, Cost: 42.96, Unpriced: 143},
			ByTool: map[aiusage.Tool]aiusage.Totals{
				aiusage.ToolOmp:    {Input: 400_000, Output: 400_000, CacheRead: 119_600_000, Requests: 600, Cost: 40.12},
				aiusage.ToolClaude: {Input: 1_000_000, Output: 60_000, CacheRead: 2_140_000, Requests: 20},
				aiusage.ToolCodex:  {Input: 1_100_000, Output: 7_000, CacheRead: 39_260_000, Requests: 6, Unpriced: 143},
			},
			Buckets: buckets,
		},
	}
}

// TestUsagePageScreenshot writes design/screens/usage-*.png with the same
// fake data the design mock uses, so the layout can be compared with
// design/usage-v1.png. Gated by SHELF_USAGE_SHOT=1 (headless PNGs from
// ui.Tester, no app loop needed); with SHELF_BASKET_SMOKE=1 — which boots
// the real app — the native notch window is screenshotted instead of the
// headless image.
func TestUsagePageScreenshot(t *testing.T) {
	headless := os.Getenv("SHELF_USAGE_SHOT") == "1"
	native := os.Getenv("SHELF_BASKET_SMOKE") == "1"
	if !headless && !native {
		t.Skip("set SHELF_USAGE_SHOT=1 (headless) or SHELF_BASKET_SMOKE=1 (native window) to write the shots")
	}
	dir := "design/screens" // shots land next to the existing design screenshots
	if _, err := os.Stat(dir); err != nil {
		t.Skipf("design/screens not found: %v", err)
	}
	now := time.Date(2026, 10, 19, 12, 0, 0, 0, time.Local)

	if native {
		a := redesignShelf(t)
		s := newUsageShot(now)
		onMain(func() {
			s.codex.q = okQuota(aiusage.ToolCodex, "Plus", now)
			s.claude.q = okQuota(aiusage.ToolClaude, "Pro", now)
			s.claude.q = windowUsed(s.claude.q, 0, 53)
			a.dataDir = s.a.dataDir
			a.settings.AIUsage.Enabled = true
			a.registerPage(s.f)
			a.modules = append(a.modules, s.f)
			a.setTab(pageUsage)
		})
		expandShelf(t, a)
		time.Sleep(500 * time.Millisecond) // let the fake fetches publish
		shotNotch(t, a, "usage-main")
		onMain(func() {
			good := okQuota(aiusage.ToolCodex, "Plus", now)
			good.FetchedAt = now.Add(-12 * time.Minute)
			s.f.quota[quotaIndex(aiusage.ToolCodex)] = usageQuotaState{
				latest: aiusage.Quota{Tool: aiusage.ToolCodex, Status: aiusage.QuotaFailed, FetchedAt: now},
				good:   good,
			}
			s.f.quota[quotaIndex(aiusage.ToolClaude)] = usageQuotaState{
				latest: aiusage.Quota{Tool: aiusage.ToolClaude, Status: aiusage.QuotaExpired, FetchedAt: now},
			}
			// A monitor tick may refetch in the real app: make the fakes
			// answer the same states so the shot can't clobber them.
			s.codex.q = s.f.quota[0].latest
			s.claude.q = s.f.quota[1].latest
			s.f.localAt = now.Add(-12 * time.Minute) // the mock's stale stamp
			a.win.Invalidate()
		})
		shotNotch(t, a, "usage-states")
		peekRestore(t, a)
		return
	}

	// Headless: stub the worker seams so the whole pipeline is synchronous.
	stubUsageSeams(t, false)
	write := func(name string, view *ui.Tester) {
		out := filepath.Join(dir, name+".png")
		file, err := os.Create(out)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		defer file.Close()
		if err := png.Encode(file, view.Image()); err != nil {
			t.Fatalf("%s encode: %v", name, err)
		}
		t.Logf("wrote %s", out)
	}
	s := newUsageShot(now)
	s.codex.q = okQuota(aiusage.ToolCodex, "Plus", now)
	s.claude.q = okQuota(aiusage.ToolClaude, "Pro", now)
	s.claude.q = windowUsed(s.claude.q, 0, 53)
	s.f.tick(now)
	write("usage-main", ui.NewTester(s.a.view, sheetW, sheetH))
	s2 := newUsageShot(now)
	good := okQuota(aiusage.ToolCodex, "Plus", now)
	good.FetchedAt = now.Add(-12 * time.Minute)
	s2.f.quota[quotaIndex(aiusage.ToolCodex)] = usageQuotaState{
		latest: aiusage.Quota{Tool: aiusage.ToolCodex, Status: aiusage.QuotaFailed, FetchedAt: now},
		good:   good,
	}
	s2.f.quota[quotaIndex(aiusage.ToolClaude)] = usageQuotaState{
		latest: aiusage.Quota{Tool: aiusage.ToolClaude, Status: aiusage.QuotaExpired, FetchedAt: now},
	}
	s2.f.tick(now)
	s2.f.localAt = now.Add(-12 * time.Minute) // the mock's stale stamp
	write("usage-states", ui.NewTester(s2.a.view, sheetW, sheetH))

	// The first-scan progress card (mock frame 3's bottom) with the two
	// other card states: 未登录 and 快用完了.
	s3 := newUsageShot(now)
	s3.scan.summaries = nil
	s3.f.quota[quotaIndex(aiusage.ToolCodex)] = usageQuotaState{
		latest: aiusage.Quota{Tool: aiusage.ToolCodex, Status: aiusage.QuotaNotLoggedIn, FetchedAt: now},
	}
	low := okQuota(aiusage.ToolClaude, "Pro", now)
	low = windowUsed(low, 0, 94)
	s3.f.quota[quotaIndex(aiusage.ToolClaude)] = usageQuotaState{latest: low, good: low}
	s3.f.localBusy = true
	s3.f.scanned = false
	s3.f.scanDone.Store(37)
	s3.f.scanTotal.Store(100)
	write("usage-scan", ui.NewTester(s3.a.view, sheetW, sheetH))
}

// The Token and 费用 cells hold one number each because the 2 × 2 grid has
// no room for the parts; resting on them explains the number. Regression:
// usageCostSub had no call site, so 「N 条没有价格」 never reached a screen.
func TestUsageKpiCellsExplainTheirNumbersOnHover(t *testing.T) {
	now := time.Date(2026, 10, 19, 12, 0, 0, 0, time.Local)
	draw := func(t *testing.T, unpriced int64) (*usageFixture, *ui.Tester) {
		t.Helper()
		fx := newUsageFixture(t, true)
		fx.codex.q = okQuota(aiusage.ToolCodex, "Plus", now)
		fx.claude.q = okQuota(aiusage.ToolClaude, "Pro", now)
		fx.scan.summaries = map[aiusage.Range]aiusage.Summary{
			aiusage.RangeToday: {
				Range: aiusage.RangeToday,
				Total: aiusage.Totals{Input: 8_400_000, Output: 1_100_000, CacheRead: 5_200_000,
					CacheWrite: 90_000, Requests: 412, Unpriced: unpriced, Cost: 3.72},
				Buckets: []aiusage.Bucket{{Start: now, Tokens: map[aiusage.Tool]int64{aiusage.ToolOmp: 1_000_000}}},
			},
		}
		fx.a.level, fx.a.visible, fx.a.tab = 2, true, pageUsage
		fx.f.tick(now)
		fx.view.Frame()
		return fx, fx.view
	}
	hover := func(t *testing.T, view *ui.Tester, label string) {
		t.Helper()
		r, ok := view.Find(label)
		if !ok {
			t.Fatalf("the %q cell is missing; texts %v", label, view.Texts())
		}
		view.Move(r.X+r.W/2, r.Y+r.H-3)
		view.Frame()
		view.Advance(time.Second)
		view.Frame()
	}
	t.Run("token split", func(t *testing.T) {
		_, view := draw(t, 3)
		hover(t, view, "Token")
		if !view.HasText("输入 8.4M · 输出 1.1M · 缓存 5.3M") {
			t.Fatalf("the Token cell has no split tip; texts %v", view.Texts())
		}
	})
	t.Run("unpriced records", func(t *testing.T) {
		_, view := draw(t, 7)
		hover(t, view, "费用")
		if !view.HasText("7 条没有价格") {
			t.Fatalf("the 费用 cell lost its unpriced tip; texts %v", view.Texts())
		}
	})
	t.Run("all priced", func(t *testing.T) {
		_, view := draw(t, 0)
		hover(t, view, "费用")
		if !view.HasText("只算自带价格") {
			t.Fatalf("the 费用 cell lost its priced-only tip; texts %v", view.Texts())
		}
	})
}

// The "was Claude Code used since the last read" answer costs a walk of
// ~/.claude/projects: it must run off the tick's thread, once at a time,
// and no faster than once a minute, however many 30ms ticks pass.
func TestUsageClaudeUsedProbeThrottledAndOffThread(t *testing.T) {
	scans, started := 0, 0
	var background []func()
	fx := newUsageFixtureWith(t, true, func(d *usageDeps) {
		d.claudeUsedSince = func(time.Time) bool { scans++; return false }
		d.claudeProbe = func(fn func()) {
			started++
			background = append(background, fn)
		}
	})
	fx.codex.q = okQuota(aiusage.ToolCodex, "Plus", *fx.now)
	fx.claude.q = okQuota(aiusage.ToolClaude, "Pro", *fx.now)
	fx.view.Click("AI 用量")
	fx.f.tick(*fx.now)
	if fx.claude.calls != 1 {
		t.Fatalf("first look calls=%d", fx.claude.calls)
	}
	// Due now: the random gap and the floor are behind it.
	st := &fx.f.quota[quotaIndex(aiusage.ToolClaude)]
	st.attemptAt = fx.now.Add(-time.Hour)
	st.nextAt = *fx.now
	base := *fx.now
	for i := 0; i < 100; i++ {
		fx.f.tick(base.Add(time.Duration(i) * 30 * time.Millisecond))
	}
	if scans != 0 {
		t.Fatalf("the projects scan ran on the tick's goroutine %d times", scans)
	}
	if started != 1 {
		t.Fatalf("100 ticks over 3s started %d projects scans, want 1", started)
	}
	background[0]() // the background scan runs
	if scans != 1 {
		t.Fatalf("the queued scan ran %d times, want 1", scans)
	}
	if !st.usedFor.Equal(st.attemptAt) {
		t.Fatalf("the scan's answer belongs to %v, want the attempt at %v", st.usedFor, st.attemptAt)
	}
	// The answer is picked up by later ticks; none of them scans again
	// inside the minute.
	for i := 0; i < 100; i++ {
		fx.f.tick(base.Add(3*time.Second + time.Duration(i)*30*time.Millisecond))
	}
	if started != 1 {
		t.Fatalf("a scan started inside the probe gap (%d)", started)
	}
	if fx.claude.calls != 1 {
		t.Fatalf("an unused allowance must not be re-read, calls=%d", fx.claude.calls)
	}
	// A minute later the tree may have changed: one fresh scan.
	fx.f.tick(base.Add(65 * time.Second))
	if started != 2 {
		t.Fatalf("no scan after the probe gap: started=%d", started)
	}
}

// An account limit is the answer, not a failed reading: it must replace the
// previous healthy numbers instead of being hidden behind them, and it
// names when the allowance returns.
func TestUsageAccountLimitReplacesLastGood(t *testing.T) {
	fx := newUsageFixture(t, true)
	fx.codex.q = okQuota(aiusage.ToolCodex, "Plus", *fx.now)
	limit := aiusage.Quota{
		Tool: aiusage.ToolClaude, Status: aiusage.QuotaLimitReached,
		FetchedAt:     fx.now.Add(31 * time.Second),
		LimitResetsAt: fx.now.Add(2*time.Hour + 7*time.Minute),
		Err:           "You've hit your session limit · resets 3pm",
	}
	fx.claude.qs = []aiusage.Quota{
		windowUsed(okQuota(aiusage.ToolClaude, "Pro", *fx.now), 0, 42),
		limit,
	}
	fx.view.Click("AI 用量")
	fx.f.tick(*fx.now)
	fx.view.Frame()
	if !fx.view.HasText("42%") {
		t.Fatalf("the healthy reading is missing; texts=%v", fx.view.Texts())
	}
	*fx.now = fx.now.Add(31 * time.Second)
	fx.view.Click("刷新")
	fx.view.Frame()
	if !fx.view.HasText("已达到额度上限") {
		t.Fatalf("the account limit is not shown; texts=%v", fx.view.Texts())
	}
	if fx.view.HasText("42%") {
		t.Fatalf("last good numbers hid the account limit; texts=%v", fx.view.Texts())
	}
	if want := usageReset(limit.LimitResetsAt, *fx.now); !fx.view.HasText(want) {
		t.Fatalf("the limit message lost the reset time %q; texts=%v", want, fx.view.Texts())
	}
}

// A transient failure — the query itself was rate limited or did not reach
// the endpoint — keeps the last good numbers, dimmed as stale data.
func TestUsageTransientFailureKeepsLastGood(t *testing.T) {
	fx := newUsageFixture(t, true)
	fx.codex.q = okQuota(aiusage.ToolCodex, "Plus", *fx.now)
	fx.claude.qs = []aiusage.Quota{
		windowUsed(okQuota(aiusage.ToolClaude, "Pro", *fx.now), 0, 42),
		{Tool: aiusage.ToolClaude, Status: aiusage.QuotaRateLimited, FetchedAt: fx.now.Add(31 * time.Second)},
	}
	fx.view.Click("AI 用量")
	fx.f.tick(*fx.now)
	*fx.now = fx.now.Add(31 * time.Second)
	fx.view.Click("刷新")
	fx.view.Frame()
	if !fx.view.HasText("42%") {
		t.Fatalf("a transient failure dropped the last good numbers; texts=%v", fx.view.Texts())
	}
	if fx.view.HasText("已达到额度上限") {
		t.Fatalf("a transient failure must not read as an account limit; texts=%v", fx.view.Texts())
	}
}

// The white pace tick follows the displayed share, as the detail bar does:
// expected used in 已用 mode, expected left in 剩余 mode.
func TestUsagePaceTickFollowsMode(t *testing.T) {
	fx := newUsageFixture(t, true)
	pi := aiusage.PaceInfo{ExpectedLeftPct: 80, LastsUntilReset: true}
	if got := fx.f.paceTickFraction(pi); math.Abs(got-0.2) > 1e-9 {
		t.Fatalf("已用 mode tick = %v, want the expected used share 0.2", got)
	}
	fx.a.settings.AIUsage.QuotaLeft = true
	if got := fx.f.paceTickFraction(pi); math.Abs(got-0.8) > 1e-9 {
		t.Fatalf("剩余 mode tick = %v, want the expected left share 0.8", got)
	}
	// An estimate outside 0–100 stops at the ends instead of leaving the
	// ring.
	fx.a.settings.AIUsage.QuotaLeft = false
	if got := fx.f.paceTickFraction(aiusage.PaceInfo{ExpectedLeftPct: 140}); got != 0 {
		t.Fatalf("over-range tick = %v, want 0", got)
	}
	if got := fx.f.paceTickFraction(aiusage.PaceInfo{ExpectedLeftPct: -20}); got != 1 {
		t.Fatalf("under-range tick = %v, want 1", got)
	}
}

// The ring's white pace tick must sit where the arc's mode puts it: at the
// expected used share in 已用 mode, at the expected left share in 剩余
// mode. The tick is the only #FFF pixel in a one-window ring, so its
// centroid angle is the fraction drawn.
func TestQuotaRingPaceTickFollowsMode(t *testing.T) {
	tickFraction := func(left bool, expectedLeftPct float64) float64 {
		t.Helper()
		fx := newUsageFixtureWith(t, true, func(d *usageDeps) {
			d.pace = func(aiusage.Window, time.Time) aiusage.PaceInfo {
				return aiusage.PaceInfo{ExpectedLeftPct: expectedLeftPct, LastsUntilReset: true}
			}
		})
		fx.a.settings.AIUsage.QuotaLeft = left
		q := okQuota(aiusage.ToolClaude, "Pro", *fx.now)
		q.Windows = q.Windows[:1] // one ring, so one tick
		// A dark card behind the ring: the standalone tester paints its own
		// background, which would otherwise be counted as tick pixels.
		view := ui.NewTester(func(c *ui.Context) {
			ui.Column(c).FillWidth().FillHeight().Background(ui.RGB(10, 10, 12)).Children(func() {
				fx.f.quotaRing(c, aiusage.ToolClaude, q, false)
			})
		}, usageRingSize, usageRingSize)
		view.Frame()
		img := view.Image()
		b := img.Bounds()
		cx, cy := float64(usageRingSize)/2, float64(usageRingSize)/2
		var sx, sy, n float64
		for y := b.Min.Y; y < b.Max.Y; y++ {
			for x := b.Min.X; x < b.Max.X; x++ {
				r, g, bl, a := img.At(x, y).RGBA()
				if a>>8 < 250 || r>>8 < 250 || g>>8 < 250 || bl>>8 < 250 {
					continue
				}
				// Only the 5-hour ring's band: nothing else is #FFF.
				if d := math.Hypot(float64(x-b.Min.X)-cx, float64(y-b.Min.Y)-cy); d < 10 || d > 20 {
					continue
				}
				sx += float64(x - b.Min.X)
				sy += float64(y - b.Min.Y)
				n++
			}
		}
		if n == 0 {
			t.Fatalf("no pace tick pixels at all (left=%v)", left)
		}
		// The tick is drawn at angle frac*2π − π/2 from the centre.
		frac := (math.Atan2(sy/n-cy, sx/n-cx) + math.Pi/2) / (2 * math.Pi)
		if frac < 0 {
			frac++
		}
		return frac
	}
	// Expected left 60% is expected used 40%: the tick must cross from one
	// reading to the other with the mode.
	if got := tickFraction(false, 60); math.Abs(got-0.4) > 0.04 {
		t.Fatalf("已用 mode tick at %.2f of the ring, want 0.40 (expected used)", got)
	}
	if got := tickFraction(true, 60); math.Abs(got-0.6) > 0.04 {
		t.Fatalf("剩余 mode tick at %.2f of the ring, want 0.60 (expected left)", got)
	}
}
