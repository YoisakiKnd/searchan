#let data = json("answer.json")
#set page(width: 180mm, height: auto, margin: (top: 14mm, bottom: 16mm, x: 13mm), fill: rgb("#f6f8fb"), numbering: none)
#set text(font: ("Noto Sans CJK SC", "Heiti SC", "Songti SC", "Arial"), size: 12pt, fill: rgb("#172639"), lang: "zh")
#set par(leading: 0.65em, justify: false)
#set heading(numbering: none)
#show heading.where(level: 2): it => block(above: 1.1em, below: 0.55em)[#text(size: 13pt, weight: "bold", fill: rgb("#245f78"), it.body)]
#let citation(c) = if c.citation != "" { [ #box(text(size: 9pt, fill: rgb("#377c96"), c.citation))] }
#let claim(c) = [#text(c.text)#citation(c)]

#let report = [
#text(size: 8.5pt, tracking: 1pt, fill: rgb("#46748b"))[SEARCHAN · 搜索报告]
#v(0.7em)
#text(size: 21pt, weight: "bold", data.title)
#v(0.3em)
#text(size: 8pt, fill: rgb("#718096"), data.timestamp + " · " + data.task_id)
#v(1em)
#block(fill: white, inset: 12pt, radius: 5pt, width: 100%)[#claim(data.lead)]

#for section in data.sections [
  #heading(level: 2, section.heading)
  #for c in section.claims [#block(above: 0.5em, below: 0.5em)[#claim(c)]]
]

#if data.table != none {
  heading(level: 2, "对比")
  let cells = data.table.columns.map(c => text(weight: "bold", c))
  for row in data.table.rows {
    for (i, cell) in row.cells.enumerate() {
      cells.push([#text(cell)#if i == row.cells.len() - 1 { citation(row) }])
    }
  }
  table(columns: data.table.columns.len(), inset: 7pt, stroke: 0.4pt + rgb("#d6e0e9"), fill: (x, y) => if y == 0 { rgb("#e8f0f6") } else { white }, ..cells)
}

#if data.uncertainties.len() > 0 [
  #heading(level: 2, "限制与待确认")
  #for item in data.uncertainties [#block(above: 0.4em)[#text(size: 10pt, fill: rgb("#725938"), item)]]
]

#if data.sources.len() > 0 [
  #heading(level: 2, "资料来源")
  #for source in data.sources [
    #block(above: 0.45em)[#text(size: 9pt, "[" + str(source.id) + "] " + source.title) \ #text(size: 8pt, fill: rgb("#718096"), source.domain)]
  ]
  #v(0.5em)
  #text(size: 8pt, fill: rgb("#718096"))[可点击链接见附带文字；检索排序不代表事实可信度。]
]
]

// Measure layout before PNG export; never rasterize an unbounded long image.
#context {
  let height = measure(block(width: 154mm, report)).height
  let max-pages = int(sys.inputs.at("max-pages", default: "3"))
  assert(height <= calc.max(470mm, max-pages * 250mm), message: "report exceeds page budget")
  let paged = height > 470mm
  page(height: if paged { 280mm } else { auto }, numbering: if paged { "1 / 1" } else { none }, report)
}
