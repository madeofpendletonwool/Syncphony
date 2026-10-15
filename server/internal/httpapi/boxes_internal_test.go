// SPDX-License-Identifier: AGPL-3.0-only

package httpapi

import (
	"testing"
	"time"
)

func TestBoxOutdated(t *testing.T) {
	for v, want := range map[string]bool{
		"v0.1.0":             true,
		"0.1.9":              true,
		"v0.2.0":             false,
		"v0.2.0-4-gabc1234":  false,
		"v0.1.0-18-g3d24587": true,
		"v1.0.0-dirty":       false,
		"v0.10.0":            false,
		"dev":                false, // unknown: not outdated
		"abc1234":            false,
	} {
		if got := boxOutdated(v); got != want {
			t.Errorf("boxOutdated(%q) = %v, want %v", v, got, want)
		}
	}
}

func TestBoxReportsAreRateLimitedAndSummarized(t *testing.T) {
	var b boxRegistry
	now := time.Now()
	temp := func(c float64) *float64 { return &c }
	r := BoxStatusReport{Version: "v1.0.0", TemperatureC: temp(50)}
	if stored, changed := b.report("d1", r, now); !stored || !changed {
		t.Fatal("first report")
	}
	// Too soon: ignored.
	if stored, _ := b.report("d1", BoxStatusReport{Version: "v9"}, now.Add(time.Second)); stored {
		t.Fatal("a report inside boxReportEvery counted")
	}
	// A degree warmer: stored, but nothing the room needs to hear.
	r.TemperatureC = temp(51)
	if stored, changed := b.report("d1", r, now.Add(boxReportEvery)); !stored || changed {
		t.Fatalf("warmer: stored=%v changed=%v", stored, changed)
	}
	// Hot: worth telling.
	r.TemperatureC = temp(81)
	if _, changed := b.report("d1", r, now.Add(2*boxReportEvery)); !changed {
		t.Fatal("running hot didn't count as a change")
	}
}

func TestValidBoxReportCutsLongErrors(t *testing.T) {
	long := make([]byte, 1000)
	for i := range long {
		long[i] = 'x'
	}
	e := string(long)
	r := BoxStatusReport{Version: "v1", Update: &BoxUpdate{At: time.Now(), Result: Failed, Error: &e}}
	if !validBoxReport(&r) || len(*r.Update.Error) != 400 {
		t.Fatalf("long error: valid=%v len=%d", validBoxReport(&r), len(*r.Update.Error))
	}
	bad := BoxStatusReport{Version: "v1", Update: &BoxUpdate{At: time.Now(), Result: "exploded"}}
	if validBoxReport(&bad) {
		t.Fatal("unknown update result accepted")
	}
}
