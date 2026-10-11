package schema

type Column struct {
	Name, Type                                                             string
	Length, Precision, Scale                                               int
	IsNullable, IsUnique, IsPrimary, IsAutoIncrement, IsUnsigned, Changing bool
	DefaultValue                                                           any
	HasDefault                                                             bool
}

func (c *Column) Nullable() *Column         { c.IsNullable = true; return c }
func (c *Column) NotNull() *Column          { c.IsNullable = false; return c }
func (c *Column) Default(value any) *Column { c.DefaultValue = value; c.HasDefault = true; return c }
func (c *Column) Unique() *Column           { c.IsUnique = true; return c }
func (c *Column) Primary() *Column          { c.IsPrimary = true; return c }
func (c *Column) AutoIncrement() *Column    { c.IsAutoIncrement = true; return c }
func (c *Column) Unsigned() *Column         { c.IsUnsigned = true; return c }
func (c *Column) Change() *Column           { c.Changing = true; return c }

type Expression string

const CurrentTimestamp Expression = "CURRENT_TIMESTAMP"
