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

var latin = map[rune]string{
	'а': "a", 'б': "b", 'в': "v", 'г': "g", 'д': "d", 'ђ': "đ", 'е': "e", 'ж': "ž", 'з': "z", 'и': "i", 'ј': "j", 'к': "k",
	'л': "l", 'љ': "lj", 'м': "m", 'н': "n", 'њ': "nj", 'о': "o", 'п': "p", 'р': "r", 'с': "s", 'т': "t", 'ћ': "ć", 'у': "u",
	'ф': "f", 'х': "h", 'ц': "c", 'ч': "č", 'џ': "dž", 'ш': "š",
}

// Latinize writes Serbian Cyrillic in Latin letters, keeping case and
// diacritics: "КОНЗУМ" → "KONZUM", "Бања Лука" → "Banja Luka", "Љубија" → "Ljubija".
func Latinize(s string) string {
	if !strings.ContainsFunc(s, func(r rune) bool { return unicode.Is(unicode.Cyrillic, r) }) {
		return s
	}
	rs := []rune(s)
	var b strings.Builder
	for i, r := range rs {
		lo := unicode.ToLower(r)
		t, ok := latin[lo]
		if !ok {
			b.WriteRune(r)
			continue
		}
		if lo != r { // upper case: "LJ" inside a shouted word, "Lj" before lower case
			nextUpper := i+1 < len(rs) && unicode.IsUpper(rs[i+1])
			prevUpper := i > 0 && unicode.IsUpper(rs[i-1])
			if nextUpper || (prevUpper && (i+1 >= len(rs) || !unicode.IsLetter(rs[i+1]))) {
				t = strings.ToUpper(t)
			} else {
				tr := []rune(t)
				t = strings.ToUpper(string(tr[0])) + string(tr[1:])
			}
		}
		b.WriteString(t)
	}
	return b.String()
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
	name = Latinize(name)
	n := taxSuffix.ReplaceAllString(name, "")
	n = strings.TrimSpace(spaces.ReplaceAllString(unitSuffix.ReplaceAllString(n, ""), " "))
	if n == "" {
		n = strings.TrimSpace(name)
	}
	return titleIfShouting(n)
}

// MerchantName is a short payee for a shop: `"TROPIC MALOPRODAJA" d.o.o. Banja Luka` → "Tropic Maloprodaja".
func MerchantName(business string) string {
	business = Latinize(business)
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

// ShopRule puts a shop in a category by words in its registered name:
// "APOTEKA ZEGIN" → the first of Categories the person has.
type ShopRule struct {
	Keywords   string   // comma separated, folded like product keywords
	Categories []string // candidate category names, best first
}

// DefaultShopRules recognise shops whose kind is in their name or brand.
// Supermarkets are left to their items.
var DefaultShopRules = []ShopRule{
	{"apoteka, ljekarna, pharmacy, farmacija, apotekarska", []string{"apoteka", "ljekarna", "pharmacy", "zdravlje", "health"}},
	{"pekara, pekarna, bakery, burekdzinica, buregdzinica, pecenjara", []string{"pekara", "bakery"}},
	{"nis petrol, gazprom, hifa, optima, benzinska, pumpa, lukoil, energopetrol, petrol bh, holdina, omv, crodux, jugopetrol, tifon", []string{"automobil", "gorivo", "fuel", "auto", "car", "transport"}},
	{"dm drogerie, dm-drogerie, drogerie markt, muller, müller, lilly drogerie, drogerija, douglas, kozmetika", []string{"kozmetika", "cosmetics", "drogerija", "beauty"}},
	{"restoran, restaurant, caffe, cafe, kafana, kafic, pizzeria, picerija, bistro, mcdonald, grill, cevabdzinica, ascinica, konoba, pivnica, kebab, burger", []string{"restoran", "restaurant", "restorani", "izlasci", "food out"}},
	{"zara, h&m, deichmann, c&a, lc waikiki, pepco, new yorker, reserved, bershka, intersport, sport vision, tom tailor", []string{"odjeca", "odjeća", "clothes", "clothing", "obuca"}},
	{"tehnomanija, technomarket, gigatron, tehnoplus, emmezeta tehnika, domod, win win, computer, kompjuter", []string{"uredjaji", "uređaji", "electronics", "tehnika"}},
	{"veterinar, vet ambulanta, pet shop, petshop, zoo shop, zoo centar", []string{"pets", "ljubimci", "kucni ljubimci"}},
	{"vinoteka, vinarija, wine, liquor, duty free", []string{"alkohol", "alcohol", "pice"}},
	{"bauhaus, bricolage, mr. bricolage, gradjevinski materijal, gvozdjara", []string{"alat", "tools", "kuca", "home"}},
}

var (
	// a drink size: "0.25", "0,5", "1.5L", "330ml", "0,33 l"
	liquidSize = regexp.MustCompile(`(?:^|[\s(])(?:0[.,]\d{1,3}|\d+(?:[.,]\d+)?\s*(?:l|lit|ml|cl|dl))(?:$|[\s)/])`)
	// words that say a group holds drinks or other liquids
	liquidWords = []string{"sok", "juice", "nektar", "napitak", "voda", "mineraln", "cola", "pivo", "vino", "rakija", "caj",
		"mlijeko", "mleko", "jogurt", "kefir", "ulje", "sirup", "ocat", "sirce", "deterdzent", "omeksivac", "sampon", "gel", "drink"}
)

// HasLiquidSize tells whether a product key carries a bottle size ("exotic
// kruska 0.25" is a pear juice, not a pear).
func HasLiquidSize(key string) bool {
	return liquidSize.MatchString(key)
}

// HoldsLiquids tells whether a group's keywords name a drink or a liquid.
func HoldsLiquids(keywords string) bool {
	for _, kw := range Keywords(keywords) {
		for _, w := range liquidWords {
			if strings.HasPrefix(kw, w) {
				return true
			}
		}
	}
	return false
}
