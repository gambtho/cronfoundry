package webapi

import "encoding/json"

// UIOverrides is the subset of schedule fields editable via the UI.
// Pointer fields: nil means "not overridden".
type UIOverrides struct {
	Cron       *string `json:"cron,omitempty"`
	Timezone   *string `json:"timezone,omitempty"`
	TimeoutSec *int32  `json:"timeout_sec,omitempty"`
	Enabled    *bool   `json:"enabled,omitempty"`
}

// applyUIOverrides returns a copy of dto with any non-nil override fields
// applied. HasUIOverrides is set when at least one field is overridden.
func applyUIOverrides(dto scheduleDTO, raw []byte) scheduleDTO {
	var ov UIOverrides
	if err := json.Unmarshal(raw, &ov); err != nil || ov == (UIOverrides{}) {
		return dto
	}
	if ov.Cron != nil {
		dto.Cron = *ov.Cron
		dto.HasUIOverrides = true
	}
	if ov.Timezone != nil {
		dto.Timezone = *ov.Timezone
		dto.HasUIOverrides = true
	}
	if ov.TimeoutSec != nil {
		dto.TimeoutSec = *ov.TimeoutSec
		dto.HasUIOverrides = true
	}
	if ov.Enabled != nil {
		dto.Enabled = *ov.Enabled
		dto.HasUIOverrides = true
	}
	return dto
}
