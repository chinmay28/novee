// Package search answers "did I ever buy X" over stored receipts.
//
// Receipt names are abbreviated ("CMND PICT STRP" for Command picture
// hanging strips), so words are compared loosely: equal, one a prefix of the
// other, both cut from the same longer word, one an abbreviation of the
// other, or one typo apart. A line that
// matches every word of the question is a full hit; a line that matches only
// some is a partial hit, ranked by how rare the words it matched are.
package search

import (
	"math"
	"sort"
	"strings"
	"unicode"

	"github.com/chinmay28/novee/skills/receipts/receipt"
)

// Query is what to look for. Every set field must hold for a line to be a hit.
type Query struct {
	Text string // words of a product name; empty means "any name"
	Item string // exact item number
	// Price bounds apply to the price paid for one unit. Nil means unbounded.
	Min, Max *receipt.Cents
}

// Hit is one receipt line that answers the query.
type Hit struct {
	Receipt receipt.Receipt
	Line    receipt.Line
	Unit    receipt.Cents // price of one unit, before discounts
	Matched []string      // the query words this line matched
	Score   float64
}

// Result groups hits. Full hits match every word; Partial hits match some.
// Both are ordered best first, then newest first.
type Result struct {
	Full, Partial []Hit
	Words         []string // the query words that were compared
	Lines         int      // how many receipt lines were looked at
}

var stop = map[string]bool{"a": true, "an": true, "the": true, "of": true, "and": true, "for": true, "with": true, "in": true}

// Words splits s into lower-case words, dropping filler words.
func Words(s string) []string {
	var out []string
	for _, w := range strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	}) {
		if !stop[w] {
			out = append(out, w)
		}
	}
	return out
}

// Similar scores how likely a and b are the same word, from 0 (no) to 1 (equal).
func Similar(a, b string) float64 {
	if a == b {
		return 1
	}
	if len(a) > len(b) {
		a, b = b, a // a is the shorter
	}
	if len(a) < 3 || isNumber(a) || isNumber(b) {
		return 0 // short codes and numbers only match exactly
	}
	if strings.HasPrefix(b, a) {
		return 0.9
	}
	if len(a) >= 5 && len(b)-len(a) <= 1 && editDistance(a, b) <= 1 {
		return 0.8
	}
	// Truncation: receipts cut long words short ("tortellon" for "tortellini").
	if n := commonPrefix(a, b); n >= 5 && 10*n >= 7*len(a) {
		return 0.8
	}
	// Abbreviation: same first letter, letters in order, and not so short
	// that it could stand for anything ("cmnd" for "command", "strp" for "strips").
	if a[0] == b[0] && 2*len(a) >= len(b)-1 && subsequence(a, b) {
		return 0.75
	}
	return 0
}

const matchAt = 0.75

// Find runs q over rs.
func Find(rs []receipt.Receipt, q Query) Result {
	res := Result{Words: Words(q.Text)}
	type cand struct {
		hit    Hit
		scores []float64 // per query word
	}
	var cands []cand
	df := make([]int, len(res.Words)) // lines matching each word

	for _, r := range rs {
		for _, l := range r.Lines {
			if l.DiscountFor != "" {
				continue // a discount row is reported with the item it reduces
			}
			res.Lines++
			unit := unitPrice(l)
			if q.Item != "" && l.Item != q.Item {
				continue
			}
			if q.Min != nil && (!l.Priced || unit < *q.Min) {
				continue
			}
			if q.Max != nil && (!l.Priced || unit > *q.Max) {
				continue
			}
			lw := Words(l.Name + " " + l.Detail)
			c := cand{hit: Hit{Receipt: r, Line: l, Unit: unit}, scores: make([]float64, len(res.Words))}
			for i, w := range res.Words {
				best := 0.0
				if w == strings.ToLower(l.Item) {
					best = 1
				}
				for _, x := range lw {
					best = math.Max(best, Similar(w, x))
				}
				if best >= matchAt {
					c.scores[i] = best
					c.hit.Matched = append(c.hit.Matched, w)
					df[i]++
				}
			}
			if len(res.Words) == 0 || len(c.hit.Matched) > 0 {
				cands = append(cands, c)
			}
		}
	}

	for _, c := range cands {
		for i, s := range c.scores {
			if s > 0 { // a word few lines contain says more than a common one
				c.hit.Score += s * math.Log(1+float64(res.Lines)/float64(df[i]))
			}
		}
		if len(c.hit.Matched) == len(res.Words) {
			res.Full = append(res.Full, c.hit)
		} else {
			res.Partial = append(res.Partial, c.hit)
		}
	}
	for _, hs := range [][]Hit{res.Full, res.Partial} {
		sort.SliceStable(hs, func(i, j int) bool {
			if hs[i].Score != hs[j].Score {
				return hs[i].Score > hs[j].Score
			}
			return hs[i].Receipt.When.After(hs[j].Receipt.When)
		})
	}
	return res
}

func unitPrice(l receipt.Line) receipt.Cents {
	if l.Qty > 1 {
		return receipt.Cents(math.Round(float64(l.Amount) / l.Qty))
	}
	return l.Amount
}

func commonPrefix(a, b string) int {
	n := 0
	for n < len(a) && n < len(b) && a[n] == b[n] {
		n++
	}
	return n
}

func isNumber(s string) bool {
	for _, r := range s {
		if !unicode.IsDigit(r) {
			return false
		}
	}
	return true
}

func subsequence(a, b string) bool {
	i := 0
	for j := 0; j < len(b) && i < len(a); j++ {
		if a[i] == b[j] {
			i++
		}
	}
	return i == len(a)
}

func editDistance(a, b string) int {
	prev := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur := make([]int, len(b)+1)
		cur[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev = cur
	}
	return prev[len(b)]
}
