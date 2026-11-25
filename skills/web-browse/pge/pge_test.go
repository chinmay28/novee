package pge

import (
	"math"
	"strings"
	"testing"
)

// A statement in the shape pdftotext gives it, with made-up names, numbers and amounts.
const sample = `                                                                                 Account No: 0000000000-0
            ENERGY STATEMENT                                                 Statement Date:    11/13/2026
            www.pge.com/MyEnergy                                                  Due Date:     12/04/2026
 Service For:                                                Your Account Summary
                                                             Amount Due on Previous Statement                                           $122.05
 A CUSTOMER                                                  Payment(s) Received Since Last Statement                                   -122.05
 1 EXAMPLE ST                                                Previous Unpaid Balance                                                       $0.00
                                                             Current PG&E Electric Monthly Charges                                       $24.23
                                                             Example Town Clean Energy Electric Generation Charges                        14.51
 Questions about your bill?                                  Current Gas Charges                                                         160.73
                                                             Total Amount Due by 12/04/2026                                            $199.47
                                                             will be reconciled on your annual True-Up statement (01/2027). No credits will
                                                             YTD NEM Charges Before Taxes                                               $262
                                                             YTD Estimated NEM Charges At True-Up                                       $255
` + "\f" + ` Summary of Your Year-to-Date (YTD) NEM Charges
Rate Schedule: Time-of-Use (Peak Pricing 4 - 9 p.m. Every Day)
   Bill Period                  Usage      Peak Usage       Net Usage      NEM Charges              Total NEM
   02/08/2026                        137             370            506           $175.82                             $185.44
   04/08/2026                         29            -309           -280            -51.11                              -53.99
   TOTAL                             166              61            226           $124.71                             $131.45
   02/08/2026                              $12.90
` + "\f" + `    Details of PG&E Electric Monthly Charges
10/07/2026 to 11/04/2026 (29 billing days)
Rate Schedule: Time-of-Use (Peak Pricing 4 - 9 p.m. Every Day)
Enrolled Programs: Net Energy Metering (NEM2)
Base Services Charge                           29 days @ $0.79343                $23.01
Example Town Utility Users' Tax (5.000%)                                           1.15                         USCA-XXXX-0100-0000
Example Town Franchise Surcharge                                                   0.07                         www.pge.com/rin
Electric Monthly Charges                                                     $24.23                             To program your smart device
                                                                                                                Imports                     709.027900 kWh
                                                                                                                Exports                    -551.193800 kWh
                                                                                                                Net Usage                   157.834100 kWh
` + "\f" + ` Details of NEM Charges
10/07/2026 to 11/04/2026 (29 billing days)                                                                      Imports                     709.027900 kWh
Enrolled Programs: Net Energy Metering (NEM2)
Baseline Allowance                       281.30                                                            kWh             (29 days x 9.7 kWh/day)
Net Usage                                                                                                                  The State Mandated
  Peak                               166.581300                                                            kWh @ $0.39757                   $66.23      (NBC) is
  Off Peak                            -8.747200                                                            kWh @ $0.36757                   -3.22
Baseline Credit                      157.834100                                                            kWh @ -$0.08140                 -12.85      fees: Public
NBC Net Usage Adjustment                                                                                                                    -1.94      Decommissioning
State Mandated Non-Bypassable Charge 1                                                                                                       8.56
Generation Credit                                                                                                                          -20.27      Usage
Power Charge Indifference Adjustment                                                                                                         5.81
Monthly NEM Charges                                                                                                                        $44.66
     Net Electric Usage This Period:157.8341 kWh 29 billing days Average Daily Usage 5.442555 kWh For 10/07 2026 kWh used off peak 9.2403
` + "\f" + ` Details of Example Town Clean Energy Electric Generation                                              Rate Identification Number
 Charges
10/07/2026 to 11/04/2026 (29 billing days)
Rate Schedule:                               NEM E-TOU-C
Generation - On Peak - Winter              166.581300 kWh @ $0.08580                $14.29
Generation - Off Peak - Winter              -8.747200 kWh @ $0.05992                 -0.52
                                             Net Charges 13.77
Local Utility Users Tax                                                               0.69
Energy Commission Surcharge                                                           0.05
` + "\f" + ` Details of Gas Charges                                                                                Meter #                    0000
10/08/2026 to 11/05/2026 (29 billing days)                                                             Current Meter Reading      3,530
Rate Schedule: G1 XB Residential Service                                                               Multiplier                 1.055206
                                                                                                       Total Usage (Est.)         53.000000 Therms
Tier 1 Allowance              11.76 Therms (24 days x 0.49 Therms/day)
Tier 1 Usage                  11.760000 Therms@ $2.42480             $28.52        10/08/2026 - 10/31/2026            $0.41294
Tier 2 Usage                  32.102070 Therms@ $2.92792              93.99        11/01/2026 - 11/05/2026            $0.52359
Gas PPP Surcharge ($0.12103 /Therm)                                    5.31
Tier 1 Usage                   7.400000 Therms@ $2.53545             $18.76
Tier 2 Usage                   1.737930 Therms@ $3.03857               5.28
Total Gas Charges                                                   $160.73
`

func near(a, b float64) bool { return math.Abs(a-b) < 0.005 }

func TestParse(t *testing.T) {
	b := Parse(sample)
	if b.Statement.Format("2006-01-02") != "2026-11-13" || b.Due.Format("2006-01-02") != "2026-12-04" || !near(b.TotalDue, 199.47) {
		t.Errorf("header: %v %v %v", b.Statement, b.Due, b.TotalDue)
	}
	if !near(b.Delivery, 24.23) || !near(b.Generation, 14.51) || b.GenerationBy != "Example Town Clean Energy" || !near(b.Gas, 160.73) {
		t.Errorf("summary: %v %v %q %v", b.Delivery, b.Generation, b.GenerationBy, b.Gas)
	}
	e := b.Electric
	if e.Days != 29 || !near(e.Imports, 709.0279) || !near(e.Exports, 551.1938) || !near(e.Net, 157.8341) || !strings.HasPrefix(e.Schedule, "Time-of-Use") {
		t.Errorf("electric: %+v", e)
	}
	if len(e.Lines) != 3 || e.Lines[0].Label != "Base Services Charge" || e.Lines[0].Unit != "days" || !near(e.Lines[0].Rate, 0.79343) || !near(e.Lines[0].Amount, 23.01) {
		t.Errorf("PG&E electric lines: %+v", e.Lines)
	}
	if !e.HasNEM || !near(e.NEMCharges, 44.66) || len(e.NEM) != 7 {
		t.Fatalf("NEM: %v %v %+v", e.HasNEM, e.NEMCharges, e.NEM)
	}
	if p := e.NEM[0]; p.Label != "Peak" || !near(p.Qty, 166.5813) || !near(p.Rate, 0.39757) || !near(p.Amount, 66.23) {
		t.Errorf("peak line: %+v", p)
	}
	if o := e.NEM[1]; o.Label != "Off Peak" || !near(o.Qty, -8.7472) || !near(o.Amount, -3.22) {
		t.Errorf("off-peak line: %+v", o)
	}
	if c := e.NEM[2]; c.Label != "Baseline Credit" || !near(c.Rate, -0.0814) || !near(c.Amount, -12.85) {
		t.Errorf("baseline credit: %+v", c)
	}
	if len(e.GenLines) != 4 || !near(e.GenLines[0].Rate, 0.0858) || !near(e.GenerationNetCharge, 13.77) || e.GenSchedule != "NEM E-TOU-C" {
		t.Errorf("generation: %+v net %v schedule %q", e.GenLines, e.GenerationNetCharge, e.GenSchedule)
	}
	g := b.GasUse
	if g.Days != 29 || !near(g.Therms, 53) || !g.Estimated || !near(g.Total, 160.73) || len(g.Lines) != 5 || !near(g.Lines[1].Rate, 2.92792) {
		t.Errorf("gas: %+v", g)
	}
	if len(b.Summary) != 3 || b.Summary[1].Label != "Example Town Clean Energy Electric Generation Charges" || !near(b.Summary[2].Amount, 160.73) {
		t.Errorf("account summary: %+v", b.Summary)
	}
	if !b.YTD.Known || !near(b.YTD.Estimate, 255) || b.YTD.TrueUp != "01/2027" || len(b.YTD.Rows) != 2 || b.YTD.Rows[1].Net != -280 || !near(b.YTD.Rows[1].Total, -53.99) {
		t.Errorf("year to date: %+v", b.YTD)
	}
}

// A month in credit: corrections and adjustments on the summary, no amount due,
// and a NEM detail that runs over two pages with two rate periods.
func TestCreditMonth(t *testing.T) {
	text := ` Service For:                                                Your Account Summary
                                                             Amount Due on Previous Statement                                           $225.47
                                                             Current PG&E Electric Monthly Charges                                       $24.23
                                                             Example Town Clean Energy Electric Generation Charges                         0.00
 Questions about your bill?                                  Current Gas Charges                                                          25.87
 Solar Hotline: 1-800-000-0000 M-F 7-6                       Gas Adjustments                                                             -46.40
 Phone: 1-800-000-0000                                       Gas Corrections                                                            -188.18
 Saturday 8 a.m.-5 p.m.                                      CREDIT BALANCE - NO PAYMENT
 www.pge.com/MyEnergy                                                                                                                  -$184.48
                                                             DUE
                                                             Current charges include a discount of $46.26 for CA Climate Credit.
` + "\f" + ` Details of NEM Charges
09/08/2026 to 10/06/2026 (29 billing days)
  Peak                               175.632500                         kWh @ $0.52240                   $91.75
  Off Peak                           154.443600                         kWh @ $0.39940                    61.68
` + "\f" + `                                Details of NEM Charges (continued)
  Peak                                55.637200                         kWh @ $0.39757                   $22.12
Generation Credit                                                                                        -12.22
Monthly NEM Charges                                                                                     $139.35
`
	b := Parse(text)
	if !near(b.CreditBalance, -184.48) || !near(b.ClimateCredit, 46.26) || b.TotalDue != 0 {
		t.Errorf("credit: %v %v %v", b.CreditBalance, b.ClimateCredit, b.TotalDue)
	}
	if len(b.Summary) != 5 || b.Summary[4].Label != "Gas Corrections" || !near(b.Summary[4].Amount, -188.18) {
		t.Errorf("summary: %+v", b.Summary)
	}
	if len(b.Electric.NEM) != 4 || !near(b.Electric.NEMCharges, 139.35) || !near(b.Electric.NEM[2].Rate, 0.39757) {
		t.Errorf("NEM over two pages: %+v %v", b.Electric.NEM, b.Electric.NEMCharges)
	}
}
