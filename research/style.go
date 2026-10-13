package research

import (
	"regexp"
	"strings"
)

var requestedIntroLength = regexp.MustCompile(`[0-9一二三四五六七八九十百千两]+\s*个?\s*(字符|字|words?\b|characters?\b)`)

// introductoryQuery applies only to an explicit, simple introduction. Compound
// requests and user-specified detail/length retain the general answer prompt.
func introductoryQuery(query string) bool {
	q := strings.ToLower(strings.TrimSpace(query))
	if len(q) > 240 || requestedIntroLength.MatchString(q) {
		return false
	}
	// An attribute's value is a fact question, not an entity overview. In
	// particular, do not replace its planned documentation query with a broad
	// project landing-page search merely because it ends in “是什么”.
	for _, marker := range []string{"默认", "级别", "参数", "返回值", "端口", "版本号", "是多少", "含义", "代表什么", "作用是什么"} {
		if strings.Contains(q, marker) {
			return false
		}
	}
	for _, marker := range []string{"区别", "比较", "对比", "关系", "为什么", "如何", "怎么", "步骤", "原理", "实现", "配置", "代码", "源码", "示例", "教程", "用法", "使用方法", "优缺点", "详细", "深入", "全面", "完整", "字数", "不少于", "至少", "分析", "列出", "分别", "以及", "同时", "并且", "；", ";", "\n"} {
		if strings.Contains(q, marker) {
			return false
		}
	}
	for _, word := range strings.FieldsFunc(q, func(r rune) bool { return r < 'a' || r > 'z' }) {
		switch word {
		case "compare", "comparison", "versus", "vs", "difference", "differences", "why", "how", "detail", "detailed", "deep", "code", "example", "examples", "steps", "words", "characters", "and":
			return false
		}
	}
	for _, marker := range []string{"简介", "简要介绍", "简单介绍", "介绍一下", "概述", "什么是", "是什么"} {
		if strings.Contains(q, marker) {
			return true
		}
	}
	if strings.HasPrefix(q, "介绍") || strings.HasSuffix(strings.TrimRight(q, "？?。.!！"), "介绍") {
		return true
	}
	for _, word := range strings.FieldsFunc(q, func(r rune) bool { return r < 'a' || r > 'z' }) {
		if word == "introduction" || word == "intro" || word == "overview" {
			return true
		}
	}
	return strings.HasPrefix(q, "what is ") || strings.HasPrefix(q, "what are ")
}

// Narrow default comparisons get a compact answer. Explicit depth, lengths,
// implementation steps and compound requests retain the general writing scope.
func conciseComparisonQuery(query string) bool {
	q := strings.ToLower(strings.TrimSpace(query))
	if len(q) > 300 || inferIntent(q) != "compare" || requestedIntroLength.MatchString(q) {
		return false
	}
	for _, marker := range []string{"详细", "深入", "全面", "完整", "至少", "不少于", "列出", "分别", "以及", "同时", "并且", "代码", "源码", "示例", "教程", "如何", "怎么", "步骤", "配置", "；", ";", "\n"} {
		if strings.Contains(q, marker) {
			return false
		}
	}
	for _, word := range strings.FieldsFunc(q, func(r rune) bool { return r < 'a' || r > 'z' }) {
		switch word {
		case "detailed", "detail", "deep", "comprehensive", "code", "example", "examples", "steps", "how":
			return false
		}
	}
	return true
}

// Focus a narrow fact/yes-no question without compressing explicit explanation,
// implementation, multi-question or length requests. This controls writing;
// retrieval and independent verification retain their normal budgets.
func focusedFactQuery(query string) bool {
	q := strings.ToLower(strings.TrimSpace(query))
	if len(q) > 480 || inferIntent(q) != "fact" || requestedIntroLength.MatchString(q) || strings.Count(q, "?")+strings.Count(q, "？") > 1 {
		return false
	}
	for _, marker := range []string{"详细", "深入", "全面", "完整", "至少", "不少于", "列出", "分别", "以及", "同时", "并且", "解释", "说明原因", "为什么", "原理", "举例", "代码", "源码", "示例", "教程", "用法", "配置方法", "步骤", "历史", "优缺点", "；", ";", "\n"} {
		if strings.Contains(q, marker) {
			return false
		}
	}
	for _, word := range strings.FieldsFunc(q, func(r rune) bool { return r < 'a' || r > 'z' }) {
		switch word {
		case "explain", "detailed", "detail", "deep", "comprehensive", "code", "example", "examples", "steps", "why", "how", "history", "list":
			return false
		}
	}
	for _, marker := range []string{"吗", "是否", "是不是", "能否", "会不会", "要不要", "需不需要", "有没有", "属于", "还是", "默认", "是多少"} {
		if strings.Contains(q, marker) {
			return true
		}
	}
	for _, prefix := range []string{"is ", "are ", "does ", "do ", "can ", "will ", "should ", "must ", "what is the default ", "which family ", "what level "} {
		if strings.HasPrefix(q, prefix) {
			return true
		}
	}
	return false
}

const factPrompt = answerEvidenceRules + `
本次是一个具体事实或是非问题。只输出完整 JSON：
{"answers_query":true,"title":"与结论一致的短标题","lead":{"text":"直接结论及必要条件","evidence_ids":["e1"]},"sections":[],"uncertainties":[]}。
按 fact_limits 只写一个段落，通常一至两句、目标不超过120个Unicode字符；没有最低字数，不为接近目标添加事实。sections=[]，不输出 table。第一句直接回答 query，只有缺少条件会让这句结论错误时才写第二句。用户明确问了相连的两个事实时两者都须回答，不能只回答其中一个。
只选择支持核心答案的最少事实。aspects 是检索线索，不能作为额外写作清单。不追加背景历史、其他对象、配置教程、内部机制、例子或另一轮总结；这些内容即使在 evidence 中出现也不自动写入答案。uncertainties 最多一条，仅说明会改变核心答案的限制。
先区分原文是在说明一般定义、某个实现、可能结果还是必要条件。不能把“做了某操作后成立”改为“只有这样才成立”，也不能把“不保证某结果”改成“必然发生相反结果”。不因资料描述某个实现流程就把它定义成所有实现都必须满足的流程。保留原文适用条件，无法确认的非必要推论直接省略。
标题与正文必须指向同一个主体和结论，标题不能比正文更确定。资料确实不足以回答核心时 answers_query=false，missing 最多四项具体缺口，lead 只说明当前可确认的部分，不编造默认值。` + "\n" + focusedFactRules

const focusedFactRules = `当前只回答 query 点名的事实，aspects 和核验提示均不是新增问题。不要追加它为何有用、不遵守会怎样、配置方式、内部机制或历史。必要条件是缺少它会使直接结论错误的条件，不是所有相关知识。
用户问“默认”时，必须区分明确规定的默认值与某些实现的常见用法；不能以教程示例或未找到其他形式证明规范默认。没有直接默认依据时，只说明所描述的形式或配置，不能下通用默认结论。`

const comparisonPrompt = answerEvidenceRules + `
本次写简洁的概念比较，只输出完整 JSON 对象，不写长篇报告：
{"answers_query":true,"title":"双方名称比较","lead":{"text":"直接说清双方最重要的区别或关系","evidence_ids":["e1","e2"]},"sections":[{"heading":"主要区别","claims":[{"text":"围绕同一个维度同时比较双方","evidence_ids":["e1","e2"]}]}],"uncertainties":[]}。
按 comparison_limits 控制范围：lead 通常50–100个字符；正文目标300–500个Unicode字符，不凑下限。只选2–3个对用户有用的区别，例如定位、职责及协作方式；最多一个节、三条结论。每条比较需有双方对应证据，不能把一方的事实反推为另一方不具备。
如果表格更清楚，可以用 table={"columns":["维度","甲","乙"],"rows":[{"cells":["定位","甲的定位","乙的定位"],"evidence_ids":["e1","e2"]}]} 代替 sections，最多三行，此时 sections=[]。结论与表格只选一种；不要重复 lead 的核心差异，不再追加概念介绍、能力清单、总结、选型建议等相似章节。
只比较用户点名的实体，aspects 是规划线索，不是用户新增的要求。别的生态项目、框架或场景除非回答本问题不可缺少，否则省略。不要把项目与所属生态、整个框架与某个数据访问子模块混为一谈。不得将双方的关系扩展为“所有生态项目都依赖它”等未被询问的全称断言；即使某篇教程这样写也省略。“只能”“默认”等强断言必须有直接证据。
uncertainties 最多一条，只写影响核心比较的必要限制；没有被问到的版本、性能数字、部署步骤和完整模块清单不用列成缺口。核心问题缺证据时 answers_query=false，missing 只列必要事实，简述可确认的部分，不扩写无关内容。`

const introductionPrompt = answerEvidenceRules + `
本次只写简洁的概念简介，不写完整研究报告。只输出 JSON：
{"answers_query":true,"title":"实体名称简介","lead":{"text":"直接说明它是什么及核心用途","evidence_ids":["e1"]},"sections":[{"heading":"核心要点","claims":[{"text":"尚未在 lead 中说过的一个核心能力","evidence_ids":["e1"]}]}],"uncertainties":[]}。
严格按 introduction_limits 控制写作范围。正文（lead 与全部 claims）通常不超过220个Unicode字符，英文字母、数字和标点各算一个字符；没有最低字数。lead 用一至两句写定义与用途；sections 可以为空，只在帮助理解核心概念时才补充一个要点节，最多两条结论。不要输出 table。
lead 已解释过的定义、目标和优势不要在 sections 中再次解释；多个重复的同义句只保留一次。先写核心定位与最重要的能力，只在有助于理解时补充资料直接支持的少量模块或用途；不逐项复制资料目录、全量数据库清单、代码、练习或案例。介绍实现架构或流程时，须说明是资料中的一种实现或常见示例，不能将它定义成所有系统的必需步骤；不需要实现细节时直接省略。
不要把某个子模块的能力、默认实现或示例写成整个项目无条件成立的特性。网页中的相关推荐、别的文章预览和评论不能代替当前文章正文。旧资料列举的项目不可直接称为当前仍维护或推荐的模块；没有现行状态证据时省略非必要例子。
uncertainties 最多一条，只说明会影响本次核心定义或用途的重要限制。用户没问的最新版本、完整模块清单、市场份额、版本对应关系，不因资料缺失就列为限制。
仍须回答 query 的核心实体。资料不足时 answers_query=false，增加最多四条具体 missing；lead 简述必要缺口，sections=[]，不把无关资料扩写成简介。`
