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
	// royale: при опасной зоне держим здоровье высоким (85/6: +78 Эло к исходным, 95/10: ещё +49, по 300 игр)
	r := DefaultParams
	r.RHungry, r.RHealth, r.Hungry = 95, 10, 85
	Profiles["royale"] = r
	// standard на 4: ищем еду, когда здоровье ниже 95 (85: +63 Эло к исходным, 95: ещё +49, по 300 игр)
	sd := DefaultParams
	sd.Hungry = 95
	Profiles["standard"] = sd
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
