package pge

import (
	"math"
	"strings"
	"testing"
)

// A statement in the shape pdftotext gives it, with made-up names, numbers and amounts.
const sample = `                                                                                 Account No: 0000000000-0
            ENERGY STATEMENT                                                 Statement Date:    11/10/2026
            www.pge.com/MyEnergy                                                  Due Date:     12/01/2026
 Service For:                                                Your Account Summary
                                                             Amount Due on Previous Statement                                           $110.00
 A CUSTOMER                                                  Payment(s) Received Since Last Statement                                   -110.00
 1 EXAMPLE ST                                                Previous Unpaid Balance                                                       $0.00
                                                             Current PG&E Electric Monthly Charges                                       $20.00
                                                             Example Town Clean Energy Electric Generation Charges                        12.00
 Questions about your bill?                                  Current Gas Charges                                                         148.00
                                                             Total Amount Due by 12/01/2026                                            $180.00
                                                             will be reconciled on your annual True-Up statement (01/2027). No credits will
                                                             YTD NEM Charges Before Taxes                                               $250
                                                             YTD Estimated NEM Charges At True-Up                                       $240
` + "\f" + ` Summary of Your Year-to-Date (YTD) NEM Charges
Rate Schedule: Time-of-Use (Peak Pricing 4 - 9 p.m. Every Day)
   Bill Period                  Usage      Peak Usage       Net Usage      NEM Charges              Total NEM
   02/05/2026                        100             300            400           $150.00                             $158.00
   04/05/2026                         20            -320           -300            -50.00                              -53.00
   TOTAL                             120             -20            100           $100.00                             $105.00
   02/05/2026                              $12.00
` + "\f" + `    Details of PG&E Electric Monthly Charges
10/01/2026 to 10/30/2026 (30 billing days)
Rate Schedule: Time-of-Use (Peak Pricing 4 - 9 p.m. Every Day)
Enrolled Programs: Net Energy Metering (NEM2)
Base Services Charge                           30 days @ $0.64000                $19.20
Example Town Utility Users' Tax (5.000%)                                           0.75                         USCA-XXXX-0100-0000
Example Town Franchise Surcharge                                                   0.05                         www.pge.com/rin
Electric Monthly Charges                                                     $20.00                             To program your smart device
                                                                                                                Imports                     700.000000 kWh
                                                                                                                Exports                    -500.000000 kWh
                                                                                                                Net Usage                   200.000000 kWh
` + "\f" + ` Details of NEM Charges
10/01/2026 to 10/30/2026 (30 billing days)                                                                      Imports                     700.000000 kWh
Enrolled Programs: Net Energy Metering (NEM2)
Baseline Allowance                       290.00                                                            kWh             (30 days x 9.7 kWh/day)
Net Usage                                                                                                                  The State Mandated
  Peak                               150.000000                                                            kWh @ $0.40000                   $60.00      (NBC) is
  Off Peak                            50.000000                                                            kWh @ $0.30000                   15.00
Baseline Credit                      200.000000                                                            kWh @ -$0.08000                 -16.00      fees: Public
NBC Net Usage Adjustment                                                                                                                    -2.00      Decommissioning
State Mandated Non-Bypassable Charge 1                                                                                                       8.00
Generation Credit                                                                                                                          -20.00      Usage
Power Charge Indifference Adjustment                                                                                                         5.00
Monthly NEM Charges                                                                                                                        $50.00
     Net Electric Usage This Period:200.0000 kWh 30 billing days Average Daily Usage 6.666667 kWh For 10/01 2026 kWh used off peak 4.0000
` + "\f" + ` Details of Example Town Clean Energy Electric Generation                                              Rate Identification Number
 Charges
10/01/2026 to 10/30/2026 (30 billing days)
Rate Schedule:                               NEM E-TOU-C
Generation - On Peak - Winter              150.000000 kWh @ $0.08000                $12.00
Generation - Off Peak - Winter              50.000000 kWh @ $0.05000                  2.50
                                             Net Charges 14.50
Local Utility Users Tax                                                               0.70
Energy Commission Surcharge                                                           0.05
` + "\f" + ` Details of Gas Charges                                                                                Meter #                    0000
10/02/2026 to 10/31/2026 (30 billing days)                                                             Current Meter Reading      1,050
Rate Schedule: G1 XB Residential Service                                                               Multiplier                 1.000000
                                                                                                       Total Usage (Est.)         50.000000 Therms
Tier 1 Allowance              12.00 Therms (24 days x 0.50 Therms/day)
Tier 1 Usage                  12.000000 Therms@ $2.00000             $24.00        10/02/2026 - 10/25/2026            $0.40000
Tier 2 Usage                  30.000000 Therms@ $3.00000              90.00        10/26/2026 - 10/31/2026            $0.50000
Gas PPP Surcharge ($0.10000 /Therm)                                    5.00
Tier 1 Usage                   6.000000 Therms@ $2.50000             $15.00
Tier 2 Usage                   2.000000 Therms@ $3.50000               7.00
Total Gas Charges                                                   $148.00
`

func near(a, b float64) bool { return math.Abs(a-b) < 0.005 }

func TestParse(t *testing.T) {
	b := Parse(sample)
	if b.Statement.Format("2006-01-02") != "2026-11-10" || b.Due.Format("2006-01-02") != "2026-12-01" || !near(b.TotalDue, 180) {
		t.Errorf("header: %v %v %v", b.Statement, b.Due, b.TotalDue)
	}
	if !near(b.Delivery, 20) || !near(b.Generation, 12.00) || b.GenerationBy != "Example Town Clean Energy" || !near(b.Gas, 148.00) {
		t.Errorf("summary: %v %v %q %v", b.Delivery, b.Generation, b.GenerationBy, b.Gas)
	}
	e := b.Electric
	if e.Days != 30 || !near(e.Imports, 700) || !near(e.Exports, 500) || !near(e.Net, 200) || !strings.HasPrefix(e.Schedule, "Time-of-Use") {
		t.Errorf("electric: %+v", e)
	}
	if len(e.Lines) != 3 || e.Lines[0].Label != "Base Services Charge" || e.Lines[0].Unit != "days" || !near(e.Lines[0].Rate, 0.64) || !near(e.Lines[0].Amount, 19.20) {
		t.Errorf("PG&E electric lines: %+v", e.Lines)
	}
	if !e.HasNEM || !near(e.NEMCharges, 50) || len(e.NEM) != 7 {
		t.Fatalf("NEM: %v %v %+v", e.HasNEM, e.NEMCharges, e.NEM)
	}
	if p := e.NEM[0]; p.Label != "Peak" || !near(p.Qty, 150) || !near(p.Rate, 0.4) || !near(p.Amount, 60) {
		t.Errorf("peak line: %+v", p)
	}
	if o := e.NEM[1]; o.Label != "Off Peak" || !near(o.Qty, 50) || !near(o.Amount, 15) {
		t.Errorf("off-peak line: %+v", o)
	}
	if c := e.NEM[2]; c.Label != "Baseline Credit" || !near(c.Rate, -0.08) || !near(c.Amount, -16) {
		t.Errorf("baseline credit: %+v", c)
	}
	if len(e.GenLines) != 4 || !near(e.GenLines[0].Rate, 0.08) || !near(e.GenerationNetCharge, 14.5) || e.GenSchedule != "NEM E-TOU-C" {
		t.Errorf("generation: %+v net %v schedule %q", e.GenLines, e.GenerationNetCharge, e.GenSchedule)
	}
	g := b.GasUse
	if g.Days != 30 || !near(g.Therms, 50) || !g.Estimated || !near(g.Total, 148.00) || len(g.Lines) != 5 || !near(g.Lines[1].Rate, 3) {
		t.Errorf("gas: %+v", g)
	}
	if len(b.Summary) != 3 || b.Summary[1].Label != "Example Town Clean Energy Electric Generation Charges" || !near(b.Summary[2].Amount, 148.00) {
		t.Errorf("account summary: %+v", b.Summary)
	}
	if !b.YTD.Known || !near(b.YTD.Estimate, 240) || b.YTD.TrueUp != "01/2027" || len(b.YTD.Rows) != 2 || b.YTD.Rows[1].Net != -300 || !near(b.YTD.Rows[1].Total, -53) {
		t.Errorf("year to date: %+v", b.YTD)
	}
}

// A month in credit: corrections and adjustments on the summary, no amount due,
// and a NEM detail that runs over two pages with two rate periods.
func TestCreditMonth(t *testing.T) {
	text := ` Service For:                                                Your Account Summary
                                                             Amount Due on Previous Statement                                           $200.00
                                                             Current PG&E Electric Monthly Charges                                       $20.00
                                                             Example Town Clean Energy Electric Generation Charges                         0.00
 Questions about your bill?                                  Current Gas Charges                                                          30.00
 Solar Hotline: 1-800-000-0000 M-F 7-6                       Gas Adjustments                                                             -40.00
 Phone: 1-800-000-0000                                       Gas Corrections                                                            -150.00
 Saturday 8 a.m.-5 p.m.                                      CREDIT BALANCE - NO PAYMENT
 www.pge.com/MyEnergy                                                                                                                  -$140.00
                                                             DUE
                                                             Current charges include a discount of $40.00 for CA Climate Credit.
` + "\f" + ` Details of NEM Charges
09/01/2026 to 09/30/2026 (30 billing days)
  Peak                               170.000000                         kWh @ $0.50000                   $85.00
  Off Peak                           150.000000                         kWh @ $0.40000                    60.00
` + "\f" + `                                Details of NEM Charges (continued)
  Peak                                50.000000                         kWh @ $0.40000                   $20.00
Generation Credit                                                                                        -12.00
Monthly NEM Charges                                                                                     $130.00
`
	b := Parse(text)
	if !near(b.CreditBalance, -140) || !near(b.ClimateCredit, 40) || b.TotalDue != 0 {
		t.Errorf("credit: %v %v %v", b.CreditBalance, b.ClimateCredit, b.TotalDue)
	}
	if len(b.Summary) != 5 || b.Summary[4].Label != "Gas Corrections" || !near(b.Summary[4].Amount, -150.00) {
		t.Errorf("summary: %+v", b.Summary)
	}
	if len(b.Electric.NEM) != 4 || !near(b.Electric.NEMCharges, 130) || !near(b.Electric.NEM[2].Rate, 0.4) {
		t.Errorf("NEM over two pages: %+v %v", b.Electric.NEM, b.Electric.NEMCharges)
	}
}
