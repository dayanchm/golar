package schema

type RenameColumn struct{ From, To string }
type Blueprint struct {
	Table                                              string
	Columns                                            []*Column
	Indexes                                            []*Index
	ForeignKeys                                        []*ForeignKey
	DroppedColumns, DroppedIndexes, DroppedForeignKeys []string
	RenamedColumns                                     []RenameColumn
}

func (b *Blueprint) add(name, kind string) *Column {
	c := &Column{Name: name, Type: kind}
	b.Columns = append(b.Columns, c)
	return c
}
func (b *Blueprint) ID() *Column { return b.BigInteger("id").Primary().AutoIncrement() }
func (b *Blueprint) String(name string, length int) *Column {
	c := b.add(name, "string")
	c.Length = length
	return c
}
func (b *Blueprint) Text(name string) *Column       { return b.add(name, "text") }
func (b *Blueprint) Integer(name string) *Column    { return b.add(name, "integer") }
func (b *Blueprint) BigInteger(name string) *Column { return b.add(name, "bigint") }
func (b *Blueprint) Boolean(name string) *Column    { return b.add(name, "boolean") }
func (b *Blueprint) Float(name string) *Column      { return b.add(name, "float") }
func (b *Blueprint) Decimal(name string, precision, scale int) *Column {
	c := b.add(name, "decimal")
	c.Precision = precision
	c.Scale = scale
	return c
}
func (b *Blueprint) Date(name string) *Column      { return b.add(name, "date") }
func (b *Blueprint) DateTime(name string) *Column  { return b.add(name, "datetime") }
func (b *Blueprint) Timestamp(name string) *Column { return b.add(name, "timestamp") }
func (b *Blueprint) JSON(name string) *Column      { return b.add(name, "json") }
func (b *Blueprint) UUID(name string) *Column      { return b.add(name, "uuid") }
func (b *Blueprint) Binary(name string) *Column    { return b.add(name, "binary") }
func (b *Blueprint) Timestamps() {
	b.Timestamp("created_at").Nullable()
	b.Timestamp("updated_at").Nullable()
}
func (b *Blueprint) SoftDeletes() { b.Timestamp("deleted_at").Nullable() }
func (b *Blueprint) Index(columns ...string) *Index {
	i := &Index{Columns: columns}
	b.Indexes = append(b.Indexes, i)
	return i
}
func (b *Blueprint) Unique(columns ...string) *Index {
	i := b.Index(columns...)
	i.Unique = true
	return i
}
func (b *Blueprint) Primary(columns ...string) *Index {
	i := b.Index(columns...)
	i.Primary = true
	return i
}
func (b *Blueprint) Foreign(columns ...string) *ForeignKey {
	f := &ForeignKey{Columns: columns, blueprint: b}
	b.ForeignKeys = append(b.ForeignKeys, f)
	return f
}
func (b *Blueprint) ForeignID(name string) *ForeignKey { b.BigInteger(name); return b.Foreign(name) }
func (b *Blueprint) DropColumn(name string)            { b.DroppedColumns = append(b.DroppedColumns, name) }
func (b *Blueprint) RenameColumn(from, to string) {
	b.RenamedColumns = append(b.RenamedColumns, RenameColumn{from, to})
}
func (b *Blueprint) DropIndex(name string) { b.DroppedIndexes = append(b.DroppedIndexes, name) }
func (b *Blueprint) DropForeign(name string) {
	b.DroppedForeignKeys = append(b.DroppedForeignKeys, name)
}
