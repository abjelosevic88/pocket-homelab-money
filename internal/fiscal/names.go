package fiscal

import (
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

var cyr = map[rune]string{
	'а': "a", 'б': "b", 'в': "v", 'г': "g", 'д': "d", 'ђ': "dj", 'е': "e", 'ж': "z", 'з': "z", 'и': "i", 'ј': "j", 'к': "k",
	'л': "l", 'љ': "lj", 'м': "m", 'н': "n", 'њ': "nj", 'о': "o", 'п': "p", 'р': "r", 'с': "s", 'т': "t", 'ћ': "c", 'у': "u",
	'ф': "f", 'х': "h", 'ц': "c", 'ч': "c", 'џ': "dz", 'ш': "s",
	'č': "c", 'ć': "c", 'ž': "z", 'š': "s", 'đ': "dj", 'ä': "a", 'ö': "o", 'ü': "u", 'é': "e", 'è': "e", 'á': "a", 'í': "i", 'ó': "o", 'ú': "u",
}

// Fold lowercases, transliterates Cyrillic and drops diacritics, so
// "МЛИЈЕКО", "Mlijeko" and "mlijeko" compare equal (and "čaj" finds "caj").
func Fold(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if t, ok := cyr[r]; ok {
			b.WriteString(t)
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}

var (
	unitSuffix = regexp.MustCompile(`(?i)\s*/\s*[\p{L}]{1,4}\.?\s*$`) // "/KO", "/KG", "/kom"
	taxSuffix  = regexp.MustCompile(`\s*\([\p{L}]{1,2}\)\s*$`)        // "(Е)"
	spaces     = regexp.MustCompile(`\s+`)
	legalForm  = regexp.MustCompile(`(?i)(^|[\s,])(d\.?\s?o\.?\s?o\.?|a\.?\s?d\.?|d\.?\s?d\.?|s\.?\s?p\.?|doo|ltd|gmbh)(?:[\s,.]|$)`)
	quoted     = regexp.MustCompile(`["“”„«»]([^"“”„«»]{2,})["“”„«»]`)
)

// ProductKey is what groups the same article across receipts: the folded
// name without the unit and tax suffixes.
func ProductKey(name string) string {
	n := taxSuffix.ReplaceAllString(name, "")
	n = unitSuffix.ReplaceAllString(n, "")
	return strings.TrimSpace(spaces.ReplaceAllString(Fold(n), " "))
}

// ProductName is a readable name for an item: "KEFIR 2.8% BALANS+/KO" → "Kefir 2.8% Balans+".
func ProductName(name string) string {
	n := taxSuffix.ReplaceAllString(name, "")
	n = strings.TrimSpace(spaces.ReplaceAllString(unitSuffix.ReplaceAllString(n, ""), " "))
	if n == "" {
		n = strings.TrimSpace(name)
	}
	return titleIfShouting(n)
}

// MerchantName is a short payee for a shop: `"TROPIC MALOPRODAJA" d.o.o. Banja Luka` → "Tropic Maloprodaja".
func MerchantName(business string) string {
	n := strings.TrimSpace(business)
	if m := quoted.FindStringSubmatch(n); m != nil {
		n = m[1]
	} else {
		if i := legalForm.FindStringIndex(n); i != nil && i[0] > 0 {
			n = n[:i[0]]
		}
		n = strings.Trim(n, ` ,.-"`)
	}
	n = strings.TrimSpace(spaces.ReplaceAllString(n, " "))
	if n == "" {
		return strings.TrimSpace(business)
	}
	return titleIfShouting(n)
}

var locationCode = regexp.MustCompile(`^\d{6,}\s*-\s*`)

// ShortLocation is the branch without the fiscal location code and the
// company name repeated in front of it:
// `10024402700780031-"TROPIC MALOPRODAJA" d.o.o. Banja Luka,PJ "TROPIC CENTAR"` → `PJ "TROPIC CENTAR"`.
func ShortLocation(location, business string) string {
	l := strings.TrimSpace(locationCode.ReplaceAllString(strings.TrimSpace(location), ""))
	if b := strings.TrimSpace(business); b != "" && strings.HasPrefix(l, b) {
		l = strings.TrimLeft(l[len(b):], " ,-")
	}
	if strings.EqualFold(l, strings.TrimSpace(business)) {
		return ""
	}
	return l
}

// titleIfShouting turns ALL CAPS into Title Case and leaves mixed case alone.
func titleIfShouting(s string) string {
	letters, upper := 0, 0
	for _, r := range s {
		if unicode.IsLetter(r) {
			letters++
			if unicode.IsUpper(r) {
				upper++
			}
		}
	}
	if letters == 0 || upper*10 < letters*8 {
		return s
	}
	words := strings.Fields(strings.ToLower(s))
	for i, w := range words {
		rs := []rune(w)
		for j, r := range rs {
			if unicode.IsLetter(r) {
				rs[j] = unicode.ToUpper(r)
				break
			}
		}
		words[i] = string(rs)
	}
	return strings.Join(words, " ")
}

// Keywords splits a group's keyword list ("mlijeko, mleko; milk") into folded words.
func Keywords(list string) []string {
	var out []string
	for _, k := range strings.FieldsFunc(list, func(r rune) bool { return r == ',' || r == ';' || r == '\n' }) {
		if k = strings.TrimSpace(Fold(k)); k != "" {
			out = append(out, k)
		}
	}
	return out
}

// MatchesKeyword tells whether a product key contains the keyword at the
// start of a word ("mlijeko" matches "trajno mlijeko 2.8%", not "xmlijeko").
func MatchesKeyword(key, kw string) bool {
	if kw == "" {
		return false
	}
	for i := 0; ; {
		j := strings.Index(key[i:], kw)
		if j < 0 {
			return false
		}
		at := i + j
		if prev, _ := utf8.DecodeLastRuneInString(key[:at]); at == 0 || !isWordRune(prev) {
			return true
		}
		i = at + 1
		if i >= len(key) {
			return false
		}
	}
}

func isWordRune(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) }

// StarterGroup is one of the suggested product groups.
type StarterGroup struct {
	Name     string
	Icon     string
	Keywords string
}

// StarterGroups are common grocery groups with Bosnian/Serbian/Croatian
// (ijekavian and ekavian) and English keywords.
var StarterGroups = []StarterGroup{
	{"Milk", "🥛", "mlijeko, mleko, milk"},
	{"Yogurt & kefir", "🥣", "jogurt, kefir, yogurt, acidofil"},
	{"Cheese", "🧀", "sir, cheese, kajmak, gauda, trapist, mozzarella, feta"},
	{"Eggs", "🥚", "jaja, jaje, eggs"},
	{"Bread & bakery", "🍞", "hljeb, hleb, kruh, pecivo, kifla, baget, lepinja, burek, bread"},
	{"Coffee", "☕", "kafa, kava, coffee, espresso, nescafe"},
	{"Tea", "🍵", "caj, tea"},
	{"Fruit", "🍎", "jabuka, jabuke, banana, banane, narandza, naranca, limun, kruska, grozdje, jagode, mandarina, kivi, breskva, sljiva, lubenica, fruit"},
	{"Vegetables", "🥕", "krompir, krumpir, luk, paradajz, rajcica, krastavac, paprika, kupus, mrkva, sargarepa, salata, tikvica, spinat, vegetable"},
	{"Meat", "🥩", "meso, piletina, pileci, junetina, svinjetina, mljeveno, mleveno, kobasica, slanina, sunka, pecenica, cevapi, meat, chicken"},
	{"Fish", "🐟", "riba, tuna, losos, sardina, oslic, fish"},
	{"Water", "💧", "voda, water, kisela"},
	{"Juice & soft drinks", "🧃", "sok, juice, cola, coca, pepsi, fanta, sprite, schweppes, ledeni caj"},
	{"Beer", "🍺", "pivo, beer, nektar, jelen, lasko, heineken, tuborg"},
	{"Wine & spirits", "🍷", "vino, wine, rakija, viski, whisky, vodka, liker"},
	{"Sweets & snacks", "🍫", "cokolada, cokoladica, keks, biskvit, bombone, cips, smoki, napolitanke, sladoled, chocolate"},
	{"Pasta, rice & flour", "🍝", "tjestenina, testenina, spageti, makaroni, riza, brasno, pasta, rice"},
	{"Oil & condiments", "🫒", "ulje, ocat, sirce, so, sol, secer, biber, kecap, majonez, senf"},
	{"Cleaning", "🧽", "deterdzent, omeksivac, sredstvo, sapun, toalet papir, ubrus, maramice, spuzva, kese za smece"},
	{"Personal care", "🧴", "sampon, gel za tusiranje, pasta za zube, cetkica, dezodorans, krema, brijac"},
	{"Bags", "🛍️", "kesa, vrecica, kesica, bag"},
	{"Cigarettes", "🚬", "cigarete, duhan, duvan, marlboro, malboro, lucky strike, camel, winston, iqos, heets, terea"},
	{"Fuel", "⛽", "eurodizel, dizel, benzin, bmb, lpg, autoplin, diesel, eurosuper"},
}
