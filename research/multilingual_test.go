package research

import (
	"testing"

	"searchan/search"
)

func TestReadingRetainsRelevantTranslatedQuery(t *testing.T) {
	plan := Plan{Queries: []string{"事务数据库的默认读取隔离级别是什么？", "transaction database default isolation level documentation"}, Aspects: []string{"默认隔离级别"}}
	hits := []hit{
		{Result: search.Result{URL: "https://blog.example/one", Title: "事务数据库的默认读取隔离级别", Snippet: "默认读取隔离级别是什么？事务数据库隔离级别说明。"}, fusion: .02, queries: []string{queryKey(plan.Queries[0])}},
		{Result: search.Result{URL: "https://mirror.example/two", Title: "事务数据库的默认读取隔离级别", Snippet: "默认读取隔离级别是什么？事务数据库隔离级别说明。"}, fusion: .019, queries: []string{queryKey(plan.Queries[0])}},
		{Result: search.Result{URL: "https://manual.example/docs/transactions", Title: "Transaction database documentation", Snippet: "The default transaction isolation level is read committed."}, fusion: .018, queries: []string{queryKey(plan.Queries[1])}},
		{Result: search.Result{URL: "https://noise.example/", Title: "Database shopping guide", Snippet: "Cheap database logos and posters."}, fusion: .017, queries: []string{queryKey(plan.Queries[1])}},
	}
	got := rankHits(plan, hits, nil, 2)
	if len(got) != 2 {
		t.Fatal("missing reading candidates")
	}
	found := false
	for _, h := range got {
		found = found || h.URL == hits[2].URL
		if h.URL == hits[3].URL {
			t.Fatal("query diversity promoted an irrelevant result")
		}
	}
	if !found {
		t.Fatal("Chinese token overlap displaced the relevant English document")
	}
	got = rankHits(plan, hits, map[string]bool{hits[2].URL: true}, 3)
	for _, h := range got {
		if h.URL == hits[2].URL {
			t.Fatal("attempted document was read again")
		}
	}
}
