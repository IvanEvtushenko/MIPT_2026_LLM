package engine

import "strings"

// Profiles — веса по режимам; заполняются из DefaultParams и проверенных локально поправок.
var Profiles = map[string]Params{}

func init() {
	for _, m := range []string{"duel", "standard", "royale", "constrictor"} {
		Profiles[m] = DefaultParams
	}
	// дуэль: хотим быть длиннее на 4, ничья дешевле, продление перебора у голов (локальные SPRT, см. README)
	d := DefaultParams
	d.LenWant, d.DrawPen, d.LenCap, d.Ext = 4, 400, 6, 2
	Profiles["duel"] = d
	// royale: при опасной зоне держим здоровье высоким (локально +78 Эло, 300 игр)
	r := DefaultParams
	r.RHungry, r.RHealth = 85, 6
	Profiles["royale"] = r
	// constrictor: честно перебираем только ближайшего соперника (+13 Эло, 1500 игр)
	c := DefaultParams
	c.MaxAdv = 1
	Profiles["constrictor"] = c
}

// ModeOf определяет режим по правилам и числу живых змеек (двое в standard — дуэль).
func ModeOf(ruleset string, alive int) string {
	switch {
	case strings.Contains(ruleset, "constrictor"):
		return "constrictor"
	case strings.Contains(ruleset, "royale"):
		return "royale"
	case alive > 2:
		return "standard"
	}
	return "duel"
}
