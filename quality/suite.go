package quality

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"regexp"
	"strings"
)

type Case struct {
	ID         string   `json:"id"`
	Query      string   `json:"query"`
	Category   string   `json:"category"`
	Split      string   `json:"split"`
	Checks     []string `json:"checks"`
	References []string `json:"references"`
}
type Suite struct {
	Version     int    `json:"version"`
	Description string `json:"description"`
	Cases       []Case `json:"cases"`
}

var caseID = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)

func readJSON(path string, limit int64, out any) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return err
	}
	if int64(len(b)) > limit {
		return errors.New("JSON file exceeds size limit")
	}
	return json.Unmarshal(b, out)
}

func Load(path string) (Suite, error) {
	var s Suite
	if err := readJSON(path, 1<<20, &s); err != nil {
		return s, err
	}
	if s.Version != 1 || len(s.Cases) == 0 || len(s.Cases) > 100 {
		return s, errors.New("invalid quality suite version or case count")
	}
	seen := map[string]bool{}
	for _, c := range s.Cases {
		if !caseID.MatchString(c.ID) || seen[c.ID] || strings.TrimSpace(c.Query) == "" || len(c.Query) > 8192 || len(c.Checks) == 0 || len(c.Checks) > 8 || len(c.References) > 8 || (c.Split != "pilot" && c.Split != "development" && c.Split != "reserve") || c.Category == "" {
			return s, errors.New("invalid or duplicate quality case")
		}
		for _, check := range c.Checks {
			if check == "" || len(check) > 2000 {
				return s, errors.New("invalid quality rubric")
			}
		}
		seen[c.ID] = true
	}
	return s, nil
}

func Select(s Suite, ids, split string, limit int) ([]Case, error) {
	if limit < 1 || limit > 100 {
		return nil, errors.New("case limit must be 1-100")
	}
	wanted := map[string]bool{}
	filter := ids != ""
	if ids != "" {
		for _, id := range strings.Split(ids, ",") {
			wanted[strings.TrimSpace(id)] = true
		}
	}
	var found []Case
	for _, c := range s.Cases {
		if (!filter || wanted[c.ID]) && (split == "all" || c.Split == split) {
			found = append(found, c)
			delete(wanted, c.ID)
		}
	}
	if len(wanted) > 0 || len(found) == 0 {
		return nil, errors.New("unknown case IDs or empty split selection")
	}
	return found[:min(limit, len(found))], nil
}
