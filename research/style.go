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

const introductionPrompt = answerEvidenceRules + `
本次只写简洁的概念简介，不写完整研究报告。只输出 JSON：
{"answers_query":true,"title":"实体名称简介","lead":{"text":"直接说明它是什么及核心用途","evidence_ids":["e1"]},"sections":[{"heading":"核心要点","claims":[{"text":"尚未在 lead 中说过的一个核心能力","evidence_ids":["e1"]}]}],"uncertainties":[]}。
严格按 introduction_limits 控制写作范围。正文（lead 与全部 claims）目标180–350个Unicode字符，英文字母、数字和标点各算一个字符；不为达到下限凑字数。lead 通常50–100个字符；最多一个要点节，最多三条补充结论。不要输出 table。
lead 已解释过的定义、目标和优势不要在 sections 中再次解释；多个重复的同义句只保留一次。优先写核心定位、最重要的能力，以及资料直接支持的2–3个代表性模块或用途；不逐项复制资料目录、全量数据库清单、代码、练习或案例。
不要把某个子模块的能力、默认实现或示例写成整个项目无条件成立的特性。网页中的相关推荐、别的文章预览和评论不能代替当前文章正文。旧资料列举的项目不可直接称为当前仍维护或推荐的模块；没有现行状态证据时省略非必要例子。
uncertainties 最多一条，只说明会影响本次核心定义或用途的重要限制。用户没问的最新版本、完整模块清单、市场份额、版本对应关系，不因资料缺失就列为限制。
仍须回答 query 的核心实体。资料不足时 answers_query=false，增加最多四条具体 missing；lead 简述必要缺口，sections=[]，不把无关资料扩写成简介。`
