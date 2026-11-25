// Package pge reads a PG&E energy statement: the text of the PDF as
// `pdftotext -layout` gives it.
//
// A statement is a few kinds of page, each with a title: the account summary,
// the PG&E electric charges, the net energy metering (NEM) detail for homes
// with solar, the electric generation charges of a community provider if
// there is one, the gas charges, and the year-to-date NEM table. Each page is
// read for the lines it is known to carry; a page or a line that is not
// there is simply left empty, because statements differ by rate and by year.
package pge

import (
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Line is one priced line of a statement: so many units at a rate, or just an amount.
type Line struct {
	Label  string
	Qty    float64 // kWh, therms or days; 0 if the line has only an amount
	Unit   string  // "kWh", "Therms", "days" or ""
	Rate   float64 // dollars per unit
	Amount float64
}

// NEMRow is one month of the year-to-date net energy metering table.
type NEMRow struct {
	End           time.Time
	Peak, OffPeak int // net kWh in peak and off-peak hours
	Net           int
	Before, Total float64 // NEM charges before and with taxes
}

// Bill is what one statement says.
type Bill struct {
	Statement, Due time.Time
	TotalDue       float64

	// The three parts of the amount due, from the account summary.
	// Summary is every line of charges and credits on the first page: the three
	// below and whatever else the month has (a true-up, a correction, an adjustment).
	Summary []Line
	// CreditBalance is set instead of TotalDue when the account is in credit: a negative amount.
	CreditBalance float64
	ClimateCredit float64 // the California Climate Credit included in the charges, as a positive amount

	Delivery     float64 // PG&E's electric charges
	Generation   float64 // electric generation by a community provider
	GenerationBy string  // its name
	Gas          float64

	Electric struct {
		From, To            time.Time
		Days                int
		Schedule            string
		Imports, Exports    float64 // kWh taken from and sent to the grid (exports are negative on the statement, positive here)
		Net                 float64
		Lines               []Line // PG&E's monthly electric charges
		NEM                 []Line // the NEM detail: net usage at its rates, credits, non-bypassable charges
		NEMCharges          float64
		HasNEM              bool
		GenLines            []Line // the provider's generation lines
		GenSchedule         string
		BaselineAllowance   float64
		AverageDailyUsage   float64
		NEMProgram          string
		ExportsKnown        bool
		GenerationNetCharge float64
	}
	GasUse struct {
		From, To  time.Time
		Days      int
		Schedule  string
		Therms    float64
		Estimated bool
		Lines     []Line
		Total     float64
	}
	YTD struct {
		Rows     []NEMRow
		Estimate float64 // estimated NEM charges at true-up
		TrueUp   string  // month of the true-up statement, "01/2027"
		Known    bool
	}
}

var (
	money      = `(-?\$?-?[\d,]+\.\d{2})`
	statement  = regexp.MustCompile(`Statement Date:\s+(\d\d/\d\d/\d{4})`)
	due        = regexp.MustCompile(`Due Date:\s+(\d\d/\d\d/\d{4})`)
	totalDue   = regexp.MustCompile(`Total Amount Due(?: by \d\d/\d\d/\d{4})?\s+` + money)
	delivery   = regexp.MustCompile(`Current PG&E Electric [A-Za-z ]*Charges\s+` + money)
	generation = regexp.MustCompile(`(?m)^.*?\s{2,}([A-Z][A-Za-z&.' ]+?) Electric Generation Charges\s+` + money)
	gas        = regexp.MustCompile(`Current Gas Charges\s+` + money)
	period     = regexp.MustCompile(`(\d\d/\d\d/\d{4}) to (\d\d/\d\d/\d{4}) \((\d+) billing days\)`)
	schedule   = regexp.MustCompile(`(?m)Rate Schedule:[ \t]+(\S.*?)(?:\s{3,}|$)`)
	usage      = regexp.MustCompile(`(Imports|Exports|Net Usage|Total Usage(?: \(Est\.\))?)\s+(-?[\d,]+\.\d+)\s*(kWh|Therms)`)
	// "Peak   166.581300   kWh @ $0.39757   $66.23", "Tier 1 Usage  11.760000 Therms@ $2.42480  $28.52"
	priced = regexp.MustCompile(`^\s*(\S.*?)\s{2,}(-?[\d,]+\.\d+)\s*(kWh|Therms)\s*@\s*(-?\$[\d.]+)\s+` + money)
	// "Base Services Charge   29 days @ $0.79343   $23.01"
	daily = regexp.MustCompile(`^\s*(\S.*?)\s{2,}(\d+) days @ \$([\d.]+)\s+` + money)
	// "Generation Credit   -20.27"
	plain     = regexp.MustCompile(`^\s*(\S.*?)\s{2,}` + money + `(?:\s{2,}.*)?$`)
	charge    = regexp.MustCompile(`(?i)(charge|credit|tax|surcharge|adjustment|discount|fee)`)
	netGen    = regexp.MustCompile(`Net Charges\s+` + money)
	baseline  = regexp.MustCompile(`Baseline Allowance\s+([\d,.]+)\s+kWh`)
	nemRow    = regexp.MustCompile(`^\s*(\d\d/\d\d/\d{4})\s+(-?[\d,]+)\s+(-?[\d,]+)\s+(-?[\d,]+)\s+` + money + `\s+` + money)
	ytd       = regexp.MustCompile(`YTD Estimated NEM Charges At True-Up\s+` + money[:len(money)-8] + `[\d,]+(?:\.\d{2})?)`)
	trueUp    = regexp.MustCompile(`True-Up statement \((\d\d/\d{4})\)`)
	program   = regexp.MustCompile(`(?m)Enrolled Programs:[ \t]+(\S.*?)(?:\s{3,}|$)`)
	creditBal = regexp.MustCompile(`CREDIT BALANCE[^$]{0,200}?(-\$[\d,]+\.\d{2})`)
	climate   = regexp.MustCompile(`discount of \$([\d,]+\.\d{2}) for CA Climate Credit`)
	summary   = regexp.MustCompile(`(?:^|\s{2,})([A-Z][A-Za-z&()'./ -]+?)\s{2,}` + money + `\s*$`)
	avgDaily  = regexp.MustCompile(`Average Daily Usage ([\d.]+) kWh`)
)

func dollars(s string) float64 {
	neg := strings.Contains(s, "-")
	f, _ := strconv.ParseFloat(strings.NewReplacer("$", "", ",", "", "-", "").Replace(s), 64)
	if neg {
		return -f
	}
	return f
}

func number(s string) float64 {
	f, _ := strconv.ParseFloat(strings.ReplaceAll(s, ",", ""), 64)
	return f
}

func date(s string) time.Time {
	t, _ := time.Parse("01/02/2006", s)
	return t
}

// lines reads the priced lines in the left part of a page.
func lines(page string) []Line {
	var out []Line
	for _, raw := range strings.Split(page, "\n") {
		if m := priced.FindStringSubmatch(raw); m != nil {
			out = append(out, Line{Label: strings.TrimSpace(m[1]), Qty: number(m[2]), Unit: m[3], Rate: dollars(m[4]), Amount: dollars(m[5])})
		} else if m := daily.FindStringSubmatch(raw); m != nil {
			out = append(out, Line{Label: strings.TrimSpace(m[1]), Qty: number(m[2]), Unit: "days", Rate: number(m[3]), Amount: dollars(m[4])})
		} else if m := plain.FindStringSubmatch(raw); m != nil && charge.MatchString(m[1]) && len(m[1]) < 70 &&
			!strings.Contains(m[1], "Statement") && !strings.Contains(m[1], "Amount Due") {
			out = append(out, Line{Label: strings.TrimSpace(m[1]), Amount: dollars(m[2])})
		}
	}
	return out
}

// Parse reads a statement from its text.
func Parse(text string) Bill {
	var b Bill
	if m := statement.FindStringSubmatch(text); m != nil {
		b.Statement = date(m[1])
	}
	if m := due.FindStringSubmatch(text); m != nil {
		b.Due = date(m[1])
	}
	if m := totalDue.FindStringSubmatch(text); m != nil {
		b.TotalDue = dollars(m[1])
	}
	if m := delivery.FindStringSubmatch(text); m != nil {
		b.Delivery = dollars(m[1])
	}
	if m := generation.FindStringSubmatch(text); m != nil {
		b.GenerationBy, b.Generation = strings.TrimSpace(m[1]), dollars(m[2])
	}
	if m := gas.FindStringSubmatch(text); m != nil {
		b.Gas = dollars(m[1])
	}
	if m := creditBal.FindStringSubmatch(text); m != nil {
		b.CreditBalance = dollars(m[1])
	}
	if m := climate.FindStringSubmatch(text); m != nil {
		b.ClimateCredit = number(m[1])
	}
	// The account summary: what follows the previous balance, down to the amount due.
	if first, _, ok := strings.Cut(text, "\f"); ok || first != "" {
		in := false
		for _, raw := range strings.Split(first, "\n") {
			switch {
			case strings.Contains(raw, "Previous Unpaid Balance") || strings.Contains(raw, "Amount Due on Previous Statement"):
				in = true
				continue
			case strings.Contains(raw, "Total Amount Due") || strings.Contains(raw, "CREDIT BALANCE") || strings.Contains(raw, "Net Energy Metering (NEM) Account Summary"):
				in = false
			}
			if m := summary.FindStringSubmatch(raw); in && m != nil && !strings.Contains(m[1], "Payment") && !strings.Contains(m[1], "Previous") {
				b.Summary = append(b.Summary, Line{Label: strings.TrimSpace(m[1]), Amount: dollars(m[2])})
			}
		}
	}
	if m := ytd.FindStringSubmatch(text); m != nil {
		b.YTD.Estimate, b.YTD.Known = dollars(m[1]), true
	}
	if m := trueUp.FindStringSubmatch(text); m != nil {
		b.YTD.TrueUp = m[1]
	}
	for _, pg := range strings.Split(text, "\f") {
		head := pg
		if len(head) > 1500 {
			head = head[:1500]
		}
		use := map[string]float64{}
		for _, m := range usage.FindAllStringSubmatch(pg, -1) {
			if _, seen := use[m[1]]; !seen {
				use[m[1]] = number(m[2])
			}
		}
		span := period.FindStringSubmatch(pg)
		rate := ""
		if m := schedule.FindStringSubmatch(pg); m != nil {
			rate = m[1]
		}
		switch {
		case strings.Contains(head, "Summary of Your Year-to-Date") && len(b.YTD.Rows) == 0:
			for _, raw := range strings.Split(pg, "\n") {
				if m := nemRow.FindStringSubmatch(raw); m != nil {
					b.YTD.Rows = append(b.YTD.Rows, NEMRow{End: date(m[1]), Peak: int(number(m[2])), OffPeak: int(number(m[3])),
						Net: int(number(m[4])), Before: dollars(m[5]), Total: dollars(m[6])})
				}
			}
		case strings.Contains(head, "Details of NEM Charges"):
			e := &b.Electric
			e.HasNEM = true
			for _, l := range lines(pg) { // the detail can run over two pages
				if strings.HasPrefix(l.Label, "Monthly NEM Charges") {
					e.NEMCharges = l.Amount // the total, not one of the lines
				} else {
					e.NEM = append(e.NEM, l)
				}
			}
			if m := baseline.FindStringSubmatch(pg); m != nil {
				e.BaselineAllowance = number(m[1])
			}
			if m := avgDaily.FindStringSubmatch(pg); m != nil {
				e.AverageDailyUsage = number(m[1])
			}
			if m := program.FindStringSubmatch(pg); m != nil {
				e.NEMProgram = m[1]
			}
			fillElectric(&b, use, span, rate)
		case strings.Contains(head, "Electric Generation") && strings.Contains(head, "Details of"):
			e := &b.Electric
			for _, l := range lines(pg) {
				switch {
				case l.Label == "Generation Charges": // the page's total
				case strings.HasPrefix(l.Label, "Generation"):
					e.GenLines = append(e.GenLines, l)
				case l.Unit == "" && !strings.Contains(l.Label, "Total") && l.Label != "Generation Charges":
					e.GenLines = append(e.GenLines, l)
				}
			}
			if m := netGen.FindStringSubmatch(pg); m != nil {
				e.GenerationNetCharge = dollars(m[1])
			}
			if e.GenSchedule = rate; rate == "" {
				if m := regexp.MustCompile(`Rate Schedule:\s*\n?\s*(\S.*?)(?:\s{3,}|\n)`).FindStringSubmatch(pg); m != nil {
					e.GenSchedule = strings.TrimSpace(m[1])
				}
			}
			fillElectric(&b, use, span, "")
		case strings.Contains(head, "Details of PG&E Electric"):
			e := &b.Electric
			for _, l := range lines(pg) {
				if !strings.Contains(l.Label, "Electric Monthly Charges") && !strings.Contains(l.Label, "Total PG&E Electric") {
					e.Lines = append(e.Lines, l)
				}
			}
			fillElectric(&b, use, span, rate)
		case strings.Contains(head, "Details of Gas Charges"):
			g := &b.GasUse
			if span != nil {
				g.From, g.To = date(span[1]), date(span[2])
				g.Days, _ = strconv.Atoi(span[3])
			}
			g.Schedule = rate
			for name, v := range use {
				if strings.HasPrefix(name, "Total Usage") {
					g.Therms = v
				}
			}
			g.Estimated = strings.Contains(pg, "Total Usage (Est.)") || strings.Contains(pg, "This is an estimated bill")
			for _, l := range lines(pg) {
				if strings.HasPrefix(l.Label, "Total Gas Charges") {
					g.Total = l.Amount
				} else {
					g.Lines = append(g.Lines, l)
				}
			}
		}
	}
	return b
}

// fillElectric takes the billing period and the meter figures from whichever
// electric page has them.
func fillElectric(b *Bill, use map[string]float64, span []string, rate string) {
	e := &b.Electric
	if span != nil && e.From.IsZero() {
		e.From, e.To = date(span[1]), date(span[2])
		e.Days, _ = strconv.Atoi(span[3])
	}
	if rate != "" && e.Schedule == "" {
		e.Schedule = rate
	}
	if v, ok := use["Imports"]; ok && e.Imports == 0 {
		e.Imports = v
	}
	if v, ok := use["Exports"]; ok && !e.ExportsKnown {
		e.ExportsKnown = true
		if v < 0 {
			v = -v
		}
		e.Exports = v
	}
	if v, ok := use["Net Usage"]; ok && e.Net == 0 {
		e.Net = v
	} else if v, ok := use["Total Usage"]; ok && e.Net == 0 {
		e.Net = v
	}
}
