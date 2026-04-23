package webapi

import (
	"encoding/json"
	"testing"
)

func TestApplyUIOverrides_AllFields(t *testing.T) {
	dto := scheduleDTO{
		Cron:       "0 9 * * MON",
		Timezone:   "UTC",
		TimeoutSec: 600,
		Enabled:    true,
	}
	raw, _ := json.Marshal(UIOverrides{
		Cron:       ptr("*/5 * * * *"),
		Timezone:   ptr("America/Los_Angeles"),
		TimeoutSec: int32ptr(120),
		Enabled:    boolptr(false),
	})
	got := applyUIOverrides(dto, raw)
	if got.Cron != "*/5 * * * *" {
		t.Errorf("cron: want */5 * * * *, got %s", got.Cron)
	}
	if got.Timezone != "America/Los_Angeles" {
		t.Errorf("timezone: want America/Los_Angeles, got %s", got.Timezone)
	}
	if got.TimeoutSec != 120 {
		t.Errorf("timeout_sec: want 120, got %d", got.TimeoutSec)
	}
	if got.Enabled {
		t.Errorf("enabled: want false, got true")
	}
	if !got.HasUIOverrides {
		t.Errorf("HasUIOverrides: want true")
	}
}

func TestApplyUIOverrides_Empty(t *testing.T) {
	dto := scheduleDTO{Cron: "0 9 * * MON", Timezone: "UTC", TimeoutSec: 600, Enabled: true}
	got := applyUIOverrides(dto, []byte(`{}`))
	if got.Cron != "0 9 * * MON" {
		t.Errorf("cron should be unchanged")
	}
	if got.HasUIOverrides {
		t.Errorf("HasUIOverrides: want false for empty overrides")
	}
}

func TestApplyUIOverrides_Partial(t *testing.T) {
	dto := scheduleDTO{Cron: "0 9 * * MON", Timezone: "UTC", TimeoutSec: 600, Enabled: true}
	raw, _ := json.Marshal(UIOverrides{Cron: ptr("0 10 * * MON")})
	got := applyUIOverrides(dto, raw)
	if got.Cron != "0 10 * * MON" {
		t.Errorf("cron: want 0 10 * * MON, got %s", got.Cron)
	}
	if got.Timezone != "UTC" {
		t.Errorf("timezone: should be unchanged")
	}
}

func ptr(s string) *string    { return &s }
func int32ptr(i int32) *int32 { return &i }
func boolptr(b bool) *bool    { return &b }
