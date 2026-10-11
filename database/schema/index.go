package schema

type Index struct {
	Name            string
	Columns         []string
	Unique, Primary bool
}

func (i *Index) Named(name string) *Index { i.Name = name; return i }

type ForeignKey struct {
	Name                                    string
	Columns, TargetColumns                  []string
	TargetTable, DeleteAction, UpdateAction string
	blueprint                               *Blueprint
}

func (f *ForeignKey) References(columns ...string) *ForeignKey { f.TargetColumns = columns; return f }
func (f *ForeignKey) On(table string) *ForeignKey              { f.TargetTable = table; return f }
func (f *ForeignKey) OnDelete(action string) *ForeignKey       { f.DeleteAction = action; return f }
func (f *ForeignKey) OnUpdate(action string) *ForeignKey       { f.UpdateAction = action; return f }
func (f *ForeignKey) CascadeOnDelete() *ForeignKey             { return f.OnDelete("cascade") }
func (f *ForeignKey) CascadeOnUpdate() *ForeignKey             { return f.OnUpdate("cascade") }
func (f *ForeignKey) Named(name string) *ForeignKey            { f.Name = name; return f }
func (f *ForeignKey) Constrained(table ...string) *ForeignKey {
	if len(table) > 0 {
		f.TargetTable = table[0]
	} else {
		f.TargetTable = f.Columns[0]
		if len(f.TargetTable) > 3 && f.TargetTable[len(f.TargetTable)-3:] == "_id" {
			f.TargetTable = f.TargetTable[:len(f.TargetTable)-3] + "s"
		}
	}
	return f.References("id")
}
