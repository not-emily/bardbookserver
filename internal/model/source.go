package model

import "time"

type Source struct {
	ID                 int64
	Name               string
	Path               string
	Enabled            bool
	WriteSidecars      bool
	DefaultMinAge      *int
	HideWhenLocked     bool
	LastScanStartedAt  *time.Time
	LastScanFinishedAt *time.Time
	CreatedAt          time.Time
}
