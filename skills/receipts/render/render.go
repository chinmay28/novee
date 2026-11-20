// Package render lays receipts out as a printable HTML document, one receipt
// per page, in the shape of the paper slip: shop, rows, totals, payments.
// It prints only what the shop reported; nothing is computed or invented.
package render

import (
	"bytes"
	"html/template"

	"github.com/chinmay28/novee/skills/receipts/receipt"
)

var tmpl = template.Must(template.New("doc").Funcs(template.FuncMap{
	"qty": func(l receipt.Line) bool { return l.Qty > 1 },
}).Parse(`<!doctype html><html><head><meta charset="utf-8"><title>Receipts</title><style>
@page { size: Letter; margin: 14mm; }
body { font: 11pt/1.35 "DejaVu Sans Mono", "Liberation Mono", Menlo, Consolas, monospace; color: #000; background: #fff; margin: 0; }
.r { width: 92mm; margin: 0 auto; page-break-after: always; }
.r:last-child { page-break-after: auto; }
h1 { font-size: 15pt; text-align: center; margin: 0 0 2mm; text-transform: uppercase; }
.c { text-align: center; }
.ret { text-align: center; font-weight: bold; margin: 2mm 0; }
table { width: 100%; border-collapse: collapse; margin: 3mm 0; }
td { padding: 0 1mm 0 0; vertical-align: top; }
td.n { white-space: nowrap; }
td.a { text-align: right; white-space: nowrap; padding-right: 0; }
tr.t td { border-top: 1px dashed #000; padding-top: 1mm; }
.d { color: #333; font-size: 9pt; }
.f { font-size: 9pt; margin-top: 3mm; }
</style></head><body>
{{range .}}<div class="r">
<h1>{{.Store}}</h1>
<div class="c">{{.Location}}</div>
{{if .Address}}<div class="c">{{.Address}}</div>{{end}}
{{if .IsReturn}}<div class="ret">*** RETURN ***</div>{{end}}
<table>
{{range .Lines}}<tr><td class="n">{{.Item}}</td><td>{{.Name}}{{if .Detail}}<div class="d">{{.Detail}}</div>{{end}}{{if qty .}}<div class="d">qty {{.Qty}}</div>{{end}}</td><td class="a">{{if .Priced}}{{.Amount}} {{.TaxFlag}}{{end}}</td></tr>
{{end}}{{if ne .Kind "online"}}<tr class="t"><td></td><td>SUBTOTAL</td><td class="a">{{.Subtotal}}</td></tr>
<tr><td></td><td>TAX</td><td class="a">{{.Tax}}</td></tr>
{{end}}<tr{{if eq .Kind "online"}} class="t"{{end}}><td></td><td><b>TOTAL</b></td><td class="a"><b>{{.Total}}</b></td></tr>
{{range .Tenders}}<tr><td></td><td>{{.Name}}</td><td class="a">{{.Amount}}</td></tr>
{{end}}</table>
<div class="c">{{.When.Format "01/02/2006 15:04"}}</div>
<div class="c f">{{.Kind}} receipt {{.ID}}</div>
<div class="c f">Printed from the shop's own records of this purchase.</div>
</div>
{{end}}</body></html>`))

// HTML returns one document with a page per receipt, in the order given.
func HTML(rs []receipt.Receipt) (string, error) {
	var b bytes.Buffer
	if err := tmpl.Execute(&b, rs); err != nil {
		return "", err
	}
	return b.String(), nil
}
