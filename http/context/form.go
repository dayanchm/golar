package context

import (
	"encoding"
	"errors"
	"fmt"
	"mime/multipart"
	"net/url"
	"reflect"
	"strconv"
	"strings"
)

var (
	textUnmarshalerType = reflect.TypeFor[encoding.TextUnmarshaler]()
	fileHeaderType      = reflect.TypeFor[*multipart.FileHeader]()
)

// decodeForm copies form values and files into the struct dst points to.
// Missing and empty values leave fields unchanged, so `validate:"required"`
// can report them.
func decodeForm(dst any, values url.Values, files map[string][]*multipart.FileHeader) error {
	v := reflect.ValueOf(dst)
	if v.Kind() != reflect.Pointer || v.IsNil() || v.Elem().Kind() != reflect.Struct {
		return errors.New("form binding needs a non-nil pointer to a struct")
	}
	return decodeStruct(v.Elem(), "", values, files)
}

func decodeStruct(v reflect.Value, prefix string, values url.Values, files map[string][]*multipart.FileHeader) error {
	t := v.Type()
	for i := range t.NumField() {
		field := t.Field(i)
		if !field.IsExported() {
			continue
		}
		name, ok := formName(field)
		if !ok {
			continue
		}
		fv := v.Field(i)
		if field.Anonymous && field.Type.Kind() == reflect.Struct && field.Tag.Get("form") == "" {
			if err := decodeStruct(fv, prefix, values, files); err != nil {
				return err
			}
			continue
		}
		key := prefix + name
		switch {
		case field.Type == fileHeaderType:
			if list := files[key]; len(list) > 0 {
				fv.Set(reflect.ValueOf(list[0]))
			}
		case field.Type == reflect.SliceOf(fileHeaderType):
			if list := files[key]; len(list) > 0 {
				fv.Set(reflect.ValueOf(list))
			}
		case field.Type.Kind() == reflect.Struct && !reflect.PointerTo(field.Type).Implements(textUnmarshalerType):
			if err := decodeStruct(fv, key+".", values, files); err != nil {
				return err
			}
		case field.Type.Kind() == reflect.Slice && field.Type.Elem().Kind() != reflect.Uint8:
			list := values[key]
			if len(list) == 0 {
				continue
			}
			slice := reflect.MakeSlice(field.Type, len(list), len(list))
			for j, value := range list {
				if err := setValue(slice.Index(j), value); err != nil {
					return fmt.Errorf("field %q: %w", key, err)
				}
			}
			fv.Set(slice)
		default:
			list := values[key]
			if len(list) == 0 || list[0] == "" && field.Type.Kind() != reflect.String {
				continue
			}
			if err := setValue(fv, list[0]); err != nil {
				return fmt.Errorf("field %q: %w", key, err)
			}
		}
	}
	return nil
}

// formName returns the form key for a field and false for fields tagged "-".
func formName(field reflect.StructField) (string, bool) {
	for _, tag := range []string{"form", "json"} {
		name, _, _ := strings.Cut(field.Tag.Get(tag), ",")
		if name == "-" {
			return "", false
		}
		if name != "" {
			return name, true
		}
	}
	return field.Name, true
}

func setValue(v reflect.Value, value string) error {
	if v.Kind() == reflect.Pointer {
		if v.IsNil() {
			v.Set(reflect.New(v.Type().Elem()))
		}
		return setValue(v.Elem(), value)
	}
	if v.CanAddr() && v.Addr().Type().Implements(textUnmarshalerType) {
		return v.Addr().Interface().(encoding.TextUnmarshaler).UnmarshalText([]byte(value))
	}
	switch v.Kind() {
	case reflect.String:
		v.SetString(value)
	case reflect.Bool:
		switch strings.ToLower(value) {
		case "on", "yes":
			v.SetBool(true)
		case "off", "no":
			v.SetBool(false)
		default:
			b, err := strconv.ParseBool(value)
			if err != nil {
				return errors.New("not a boolean")
			}
			v.SetBool(b)
		}
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		n, err := strconv.ParseInt(value, 10, v.Type().Bits())
		if err != nil {
			return errors.New("not an integer in range")
		}
		v.SetInt(n)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		n, err := strconv.ParseUint(value, 10, v.Type().Bits())
		if err != nil {
			return errors.New("not an unsigned integer in range")
		}
		v.SetUint(n)
	case reflect.Float32, reflect.Float64:
		n, err := strconv.ParseFloat(value, v.Type().Bits())
		if err != nil {
			return errors.New("not a number")
		}
		v.SetFloat(n)
	default:
		return fmt.Errorf("unsupported type %s", v.Type())
	}
	return nil
}
