package classroom

import "strings"

const (
	MaxSearchCalls       = 3
	MaxSearchResults     = 5
	MaxSearchResultBytes = 64 * 1024
)

var freshnessSignals = []string{
	"最新", "目前", "当前", "今天", "今年", "近期", "实时", "价格", "政策", "版本", "新闻",
}

func ShouldSearch(requirement string) bool {
	requirement = strings.ToLower(requirement)
	for _, signal := range freshnessSignals {
		if strings.Contains(requirement, signal) {
			return true
		}
	}
	return false
}
