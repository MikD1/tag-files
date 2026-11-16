package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

type inspectionMount struct {
	Index                        int
	Type, Source, Name, Identity string
}
type inspectionPlan struct {
	InputSHA256, TargetLeaf string
	Mounts                  []inspectionMount
}
type inspectionResult struct {
	InputSHA256, TargetIdentity string
	Mounts                      []inspectionMount
}

func mountKey(m mount) string { return m.Type + "\x00" + m.Source + "\x00" + m.Name }
func mountIdentity(m mount) string {
	if m.Canonical != "" {
		return m.Canonical
	}
	return m.Source
}
func rawMount(m mount) mount { m.Canonical = ""; return m }
func decodeMountPath(value string) (string, error) {
	var b strings.Builder
	for i := 0; i < len(value); i++ {
		if value[i] != '\\' {
			b.WriteByte(value[i])
			continue
		}
		if i+3 >= len(value) {
			return "", errors.New("invalid daemon mountinfo escape")
		}
		n, e := strconv.ParseUint(value[i+1:i+4], 8, 8)
		if e != nil {
			return "", errors.New("invalid daemon mountinfo escape")
		}
		b.WriteByte(byte(n))
		i += 3
	}
	return b.String(), nil
}
func canonicalInspection(plan inspectionPlan, reader io.Reader) (inspectionResult, error) {
	identities := map[string]string{}
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 4096), 1<<20)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 6 {
			return inspectionResult{}, errors.New("invalid daemon mountinfo")
		}
		point, e := decodeMountPath(fields[4])
		if e != nil {
			return inspectionResult{}, e
		}
		if !strings.HasPrefix(point, "/inspection/") {
			continue
		}
		root, e := decodeMountPath(fields[3])
		if e != nil {
			return inspectionResult{}, e
		}
		if !filepath.IsAbs(root) || !regexp.MustCompile(`^[0-9]+:[0-9]+$`).MatchString(fields[2]) {
			return inspectionResult{}, errors.New("invalid physical native mount identity")
		}
		identity := filepath.Clean("/@device/" + fields[2] + "/" + strings.TrimPrefix(root, "/"))
		if _, exists := identities[point]; exists {
			return inspectionResult{}, errors.New("ambiguous inspection mount")
		}
		identities[point] = identity
	}
	if e := scanner.Err(); e != nil {
		return inspectionResult{}, e
	}
	parent := identities["/inspection/target-parent"]
	if parent == "" || plan.TargetLeaf == "" || filepath.Base(plan.TargetLeaf) != plan.TargetLeaf {
		return inspectionResult{}, errors.New("target physical parent identity unavailable")
	}
	result := inspectionResult{InputSHA256: plan.InputSHA256, TargetIdentity: filepath.Join(parent, plan.TargetLeaf), Mounts: append([]inspectionMount(nil), plan.Mounts...)}
	for i := range result.Mounts {
		entry := &result.Mounts[i]
		entry.Identity = identities[fmt.Sprintf("/inspection/mount-%d", entry.Index)]
		if entry.Identity == "" {
			return inspectionResult{}, errors.New("physical mount identity unavailable")
		}
	}
	return result, nil
}
func mountSources(o options) error {
	raw, e := regular(o.Inspect, 128<<20)
	if e != nil {
		return e
	}
	var rows []container
	if e = json.Unmarshal(raw, &rows); e != nil {
		return e
	}
	plan := inspectionPlan{InputSHA256: digest(raw), TargetLeaf: filepath.Base(o.TargetDirectory)}
	seen := map[string]bool{}
	var protocol strings.Builder
	argumentBytes := 0
	for _, row := range rows {
		working := filepath.Clean(row.Config.Labels["com.docker.compose.project.working_dir"])
		if !row.State.Running && !row.State.Paused && !row.State.Restarting && working != o.SourceDirectory && working != o.TargetDirectory {
			continue
		}
		for _, m := range row.Mounts {
			if m.Type != "bind" && m.Type != "volume" {
				continue
			}
			key := mountKey(m)
			if seen[key] {
				continue
			}
			seen[key] = true
			if !safeValue(m.Source) || !filepath.IsAbs(m.Source) {
				return errors.New("native inspection mount paths must be bounded absolute paths without commas or newlines")
			}
			source := m.Source
			if m.Type == "volume" {
				source = m.Name
				if !regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]*$`).MatchString(source) {
					return errors.New("unsafe inspection volume name")
				}
			}
			argumentBytes += len(source) + 128
			if len(plan.Mounts) >= 256 || argumentBytes > 65536 {
				return errors.New("too many native mounts for bounded migration inspection")
			}
			index := len(plan.Mounts)
			plan.Mounts = append(plan.Mounts, inspectionMount{Index: index, Type: m.Type, Source: m.Source, Name: m.Name})
			fmt.Fprintf(&protocol, "MOUNT_KIND=%s\nMOUNT_SOURCE=%s\nMOUNT_INDEX=%d\n", m.Type, source, index)
		}
	}
	if e = saveJSON(filepath.Join(o.State, "inspection-plan.json"), plan); e != nil {
		return e
	}
	if e = os.Chmod(filepath.Join(o.State, "inspection-plan.json"), 0444); e != nil {
		return e
	}
	return atomicFile(filepath.Join(o.State, "inspection-mounts.env"), []byte(protocol.String()), 0600)
}
func canonicalMounts(o options) error {
	b, e := regular(o.Plan, 1<<20)
	if e != nil {
		return e
	}
	var plan inspectionPlan
	if e = json.Unmarshal(b, &plan); e != nil {
		return e
	}
	f, e := os.Open("/proc/self/mountinfo")
	if e != nil {
		return e
	}
	defer f.Close()
	result, e := canonicalInspection(plan, f)
	if e != nil {
		return e
	}
	return json.NewEncoder(os.Stdout).Encode(result)
}
func loadCanonical(inspect string) (inspectionResult, error) {
	var result inspectionResult
	b, e := regular(filepath.Join(filepath.Dir(inspect), "canonical-mounts.json"), 4<<20)
	if e != nil {
		return result, e
	}
	if e = json.Unmarshal(b, &result); e != nil {
		return result, e
	}
	raw, e := regular(inspect, 128<<20)
	if e != nil {
		return result, e
	}
	if result.InputSHA256 != digest(raw) {
		return result, errors.New("daemon inspection mapping is stale")
	}
	return result, nil
}
func targetIdentity(inspect, fallback string) (string, error) {
	mapping, e := loadCanonical(inspect)
	if os.IsNotExist(e) {
		return fallback, nil
	}
	if e != nil {
		return "", e
	}
	return mapping.TargetIdentity, nil
}
func rawSourceContainers(path, directory string) ([]byte, error) {
	b, e := regular(path, 128<<20)
	if e != nil {
		return nil, e
	}
	var raw []json.RawMessage
	if e = json.Unmarshal(b, &raw); e != nil {
		return nil, e
	}
	selected := []json.RawMessage{}
	for _, r := range raw {
		var c container
		if e = json.Unmarshal(r, &c); e != nil {
			return nil, e
		}
		if filepath.Clean(c.Config.Labels["com.docker.compose.project.working_dir"]) == directory {
			selected = append(selected, r)
		}
	}
	return json.MarshalIndent(selected, "", "  ")
}
