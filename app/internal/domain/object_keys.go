package domain

import "strings"

// OwnedObjectKey recognizes only canonical namespaces owned by an explicit
// library/asset-or-upload identity, never a filename or a loose prefix match.
func OwnedObjectKey(library, id, role, key string) bool {
	if ValidateUUID(library) != nil || ValidateUUID(id) != nil || len(key) > 1024 || strings.ContainsAny(key, "\\\x00\r\n") {
		return false
	}
	for _, part := range strings.Split(key, "/") {
		if part == "" || part == "." || part == ".." {
			return false
		}
		for _, r := range part {
			if r < 33 || r > 126 {
				return false
			}
		}
	}
	switch role {
	case "incoming":
		return key == "incoming/"+library+"/"+id+"/source"
	case "primary":
		prefix := "primary/" + library + "/" + id + "/media."
		return strings.HasPrefix(key, prefix) && len(key) > len(prefix) && !strings.Contains(strings.TrimPrefix(key, prefix), "/")
	case "poster":
		prefix := "previews/" + library + "/" + id + "/"
		if !strings.HasPrefix(key, prefix) {
			return false
		}
		rest := strings.Split(strings.TrimPrefix(key, prefix), "/")
		return len(rest) == 2 && rest[1] == "poster.jpg"
	}
	return false
}
