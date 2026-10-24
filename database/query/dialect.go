package query

import "strconv"

// dialect holds the SQL differences between supported databases.
type dialect struct {
	name          string
	quote         string
	maxIdentifier int // bytes per identifier component; 0 means no check
	numbered      bool
	// offsetOnly is emitted before OFFSET when no LIMIT is set; "" means OFFSET may stand alone.
	offsetOnly string
}

var dialects = map[string]*dialect{
	"sqlite":   {name: "sqlite", quote: `"`, offsetOnly: " LIMIT -1"},
	"postgres": {name: "postgres", quote: `"`, maxIdentifier: 63, numbered: true},
	"mysql":    {name: "mysql", quote: "`", maxIdentifier: 64, offsetOnly: " LIMIT 18446744073709551615"},
}

func (d *dialect) placeholder(n int) string {
	if d.numbered {
		return "$" + strconv.Itoa(n)
	}
	return "?"
}
