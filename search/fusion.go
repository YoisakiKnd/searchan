package search

import "strings"

// SourceFamilies identifies only known shared upstreams for rank fusion.
// Unknown providers remain distinct; this is never a factual-confidence score.
func SourceFamilies(name string, sources []Source) []string {
	if name == "ddg" {
		return []string{"duckduckgo"}
	}
	if name != "searxng" {
		return []string{name}
	}
	var families []string
	for _, source := range sources {
		for _, engine := range source.Engines {
			family := strings.ToLower(strings.TrimSpace(engine))
			switch family {
			case "duckduckgo", "duckduckgo lite":
				family = "duckduckgo"
			case "brave":
				family = "brave"
			default:
				family = "searxng:" + family
			}
			seen := false
			for _, f := range families {
				if f == family {
					seen = true
					break
				}
			}
			if !seen && len(families) < 8 {
				families = append(families, family)
			}
		}
	}
	if len(families) == 0 {
		families = append(families, "searxng")
	}
	return families
}
