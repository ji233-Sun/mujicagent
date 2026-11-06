package model

import "time"

type Release struct {
	Tag        string    `json:"tag_name"`
	Notes      string    `json:"body"`
	Published  time.Time `json:"published_at"`
	Draft      bool      `json:"draft"`
	Prerelease bool      `json:"prerelease"`
}
