package model

import "time"

type Collection struct {
	ID          int64
	OwnerID     *int64
	Name        string
	Description string
	CreatedAt   time.Time
}

func (c *Collection) IsServerCollection() bool { return c.OwnerID == nil }
