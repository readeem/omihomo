package core

import "slices"

// FilterSelections retains choices that still name a member of a select group.
func FilterSelections(p Profile, saved map[string]string) map[string]string {
	selected := map[string]string{}
	for _, group := range p.Groups {
		if member, ok := saved[group.Name]; ok && group.Type == "select" && slices.Contains(group.Members, member) {
			selected[group.Name] = member
		}
	}
	return selected
}
