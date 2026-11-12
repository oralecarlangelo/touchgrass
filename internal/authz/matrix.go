package authz

import "github.com/oralecarlangelo/touchgrass/internal/model"

// MatrixRow is one capability as the access overview shows it: where it
// applies, what it covers, and which roles hold it.
type MatrixRow struct {
	Capability Capability
	Scope      Scope
	Summary    string
	// Grants says, for every role in Roles, whether it holds the
	// capability before any scope is considered.
	Grants map[model.Role]bool
	// Limits holds a sentence for each role whose grant is narrower
	// than the capability's name says.
	Limits map[model.Role]string
}

// Roles returns the roles an account may be given, from the least to
// the most powerful.
func Roles() []model.Role {
	return []model.Role{model.RoleViewer, model.RoleDeveloper, model.RoleAdmin}
}

// Matrix returns the whole policy, one row per capability in matrix
// order. It is read from the table enforcement uses, so what an admin
// is shown cannot differ from what a guard decides.
func Matrix() []MatrixRow {
	rows := make([]MatrixRow, 0, len(scopes))

	for _, entry := range scopes {
		row := MatrixRow{
			Capability: entry.capability,
			Scope:      entry.scope,
			Summary:    entry.summary,
			Grants:     map[model.Role]bool{},
			Limits:     map[model.Role]string{},
		}

		for _, role := range Roles() {
			row.Grants[role] = RoleHolds(role, entry.capability)

			if limit, ok := limits[role][entry.capability]; ok {
				row.Limits[role] = limit
			}
		}

		rows = append(rows, row)
	}

	return rows
}
