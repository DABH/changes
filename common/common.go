// Package common contains common code for backend and frontend.
package common

import (
	"github.com/shurcooL/users"
)

type State struct {
	BaseURI          string
	ReqPath          string
	RepoSpec         string
	ChangeID         uint64 `json:",omitempty"` // ChangeID is the current change ID, or 0 if not applicable (e.g., current page is /changes).
	CurrentUser      users.User
	DisableReactions bool
	DisableUsers     bool
}
