package database

import (
	"database/sql"
	"encoding"
	"encoding/base64"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// MaxResultBytes 是保留结果的保守计费上限，也是 JSON/protobuf 响应的硬上限。
// 它明显低于默认 4 MiB gRPC 接收上限，不限制驱动读取单个值时的峰值内存。
const MaxResultBytes = 1 << 20

// 每个节点/键额外计费覆盖容器、JSON 标点、protobuf 包装和保留值结构。
// 这不是序列化长度的精确值；达到上限前也可能拒绝或截断结果。
const resultNodeCost = 64

type resultBudget struct{ remaining int }

// ResultBudget 供协议层在创建第二份结果树之前增量预检。
// AddValue(nil) 为一个容器/标量计费，键和文本通过 AddValue(string) 计费。
type ResultBudget struct{ budget resultBudget }

func NewResultBudget() *ResultBudget {
	return &ResultBudget{budget: resultBudget{remaining: MaxResultBytes}}
}

func (b *ResultBudget) AddValue(value any) error {
	return b.budget.value(reflect.ValueOf(value), 0)
}

func resultTooLarge() error {
	return &DatabaseError{Code: CodeResultTooLarge, Operation: "result"}
}

func (b *resultBudget) take(size int) error {
	if size < 0 || size > b.remaining {
		return resultTooLarge()
	}
	b.remaining -= size
	return nil
}

// ValidateResultBudget 不序列化或复制值，先限制数据库响应的累计体积。
// 自定义 JSON 编码器不在协议范围内，避免预检之后执行无界 MarshalJSON。
func ValidateResultBudget(value any) error {
	_, err := newResultBudget(value)
	return err
}

func newResultBudget(value any) (*resultBudget, error) {
	budget := &resultBudget{remaining: MaxResultBytes}
	if err := budget.value(reflect.ValueOf(value), 0); err != nil {
		return nil, err
	}
	return budget, nil
}

var (
	resultTimeType      = reflect.TypeFor[time.Time]()
	resultJSONMarshaler = reflect.TypeFor[json.Marshaler]()
	resultTextMarshaler = reflect.TypeFor[encoding.TextMarshaler]()
)

func resultHasMarshaler(value reflect.Type) bool {
	return value.Implements(resultJSONMarshaler) || value.Implements(resultTextMarshaler)
}

func (b *resultBudget) value(value reflect.Value, depth int) error {
	if depth > 32 {
		return resultTooLarge()
	}
	if !value.IsValid() {
		return b.take(resultNodeCost)
	}
	if value.Kind() == reflect.Interface || value.Kind() == reflect.Pointer {
		if value.IsNil() {
			return b.take(resultNodeCost)
		}
		// 指针上的自定义编码也不能绕过预检。
		if value.Type() != reflect.PointerTo(resultTimeType) && resultHasMarshaler(value.Type()) {
			return errors.New("unsupported database result marshaler")
		}
		return b.value(value.Elem(), depth+1)
	}
	if value.Type() == resultTimeType {
		return b.take(resultNodeCost + 48)
	}
	if resultHasMarshaler(value.Type()) || resultHasMarshaler(reflect.PointerTo(value.Type())) {
		return errors.New("unsupported database result marshaler")
	}
	switch value.Kind() {
	case reflect.String:
		return b.text(value.String())
	case reflect.Bool, reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
		reflect.Float32, reflect.Float64:
		return b.take(resultNodeCost)
	case reflect.Array, reflect.Slice:
		if value.Type().Elem().Kind() == reflect.Uint8 && value.Kind() == reflect.Slice {
			// 命名 byte 元素若有编码器，encoding/json 会逐元素调用，不能按 base64 预检。
			if resultHasMarshaler(value.Type().Elem()) || resultHasMarshaler(reflect.PointerTo(value.Type().Elem())) {
				return errors.New("unsupported database result byte marshaler")
			}
			// JSON 对 []byte 使用 base64；先做长度检查再计算，避免整数溢出。
			if value.Len() > b.remaining {
				return resultTooLarge()
			}
			return b.take(resultNodeCost + base64.StdEncoding.EncodedLen(value.Len()))
		}
		if err := b.take(resultNodeCost); err != nil {
			return err
		}
		for index := 0; index < value.Len(); index++ {
			if err := b.value(value.Index(index), depth+1); err != nil {
				return err
			}
		}
		return nil
	case reflect.Map:
		if value.Type().Key().Kind() != reflect.String || resultHasMarshaler(value.Type().Key()) {
			return errors.New("unsupported database result map key")
		}
		if err := b.take(resultNodeCost); err != nil {
			return err
		}
		iterator := value.MapRange()
		for iterator.Next() {
			if err := b.text(iterator.Key().String()); err != nil {
				return err
			}
			if err := b.value(iterator.Value(), depth+1); err != nil {
				return err
			}
		}
		return nil
	case reflect.Struct:
		if err := b.take(resultNodeCost); err != nil {
			return err
		}
		for index := 0; index < value.NumField(); index++ {
			field := value.Type().Field(index)
			// encoding/json 会提升未导出的匿名结构中的导出字段，不能跳过预检。
			if !field.IsExported() && !field.Anonymous {
				continue
			}
			tag := field.Tag.Get("json")
			if tag == "-" {
				continue
			}
			name, options, _ := strings.Cut(tag, ",")
			// 当前响应模型只使用 omitempty；string 会双重转义，omitzero
			// 还会调用任意 IsZero 方法，不能在无界编码发生后才检查。
			for option := range strings.SplitSeq(options, ",") {
				if option != "" && option != "omitempty" {
					return errors.New("unsupported database result JSON option")
				}
			}
			if name != "" && !validResultJSONTag(name) {
				return errors.New("unsupported database result JSON tag")
			}
			fieldValue := value.Field(index)
			if strings.Contains(","+options+",", ",omitempty,") && emptyResultField(fieldValue) {
				continue
			}
			if name == "" {
				name = field.Name
			}
			if err := b.text(name); err != nil {
				return err
			}
			if err := b.value(fieldValue, depth+1); err != nil {
				return err
			}
		}
		return nil
	default:
		return errors.New("unsupported database result value")
	}
}

func emptyResultField(value reflect.Value) bool {
	switch value.Kind() {
	case reflect.Array, reflect.Map, reflect.Slice, reflect.String:
		return value.Len() == 0
	case reflect.Bool, reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
		reflect.Float32, reflect.Float64, reflect.Interface, reflect.Pointer:
		return value.IsZero()
	}
	return false
}

func (b *resultBudget) text(value string) error {
	if len(value) > b.remaining {
		return resultTooLarge()
	}
	if err := b.take(resultNodeCost); err != nil {
		return err
	}
	for index := 0; index < len(value); {
		width, cost := escapedResultByte(value[index:])
		if err := b.take(cost); err != nil {
			return err
		}
		index += width
	}
	return nil
}

// 与 encoding/json 默认 HTML 转义一致，并计入无效 UTF-8 和 U+2028/2029。
func escapedResultByte(value string) (width, cost int) {
	first := value[0]
	if first < utf8.RuneSelf {
		switch first {
		case '\\', '"', '\n', '\r', '\t', '\b', '\f':
			return 1, 2
		case '<', '>', '&':
			return 1, 6
		default:
			if first < 0x20 {
				return 1, 6
			}
			return 1, 1
		}
	}
	runeValue, size := utf8.DecodeRuneInString(value)
	if runeValue == utf8.RuneError && size == 1 {
		return 1, 6
	}
	if runeValue == '\u2028' || runeValue == '\u2029' {
		return size, 6
	}
	return size, size
}

func (b *resultBudget) bytesText(value []byte) error {
	if len(value) > b.remaining {
		return resultTooLarge()
	}
	if err := b.take(resultNodeCost); err != nil {
		return err
	}
	// 不先 string(value)：大文本、精确小数和控制字符必须在复制前计费。
	for index := 0; index < len(value); {
		first := value[index]
		width, cost := 1, 1
		if first < utf8.RuneSelf {
			switch first {
			case '\\', '"', '\n', '\r', '\t', '\b', '\f':
				cost = 2
			case '<', '>', '&':
				cost = 6
			default:
				if first < 0x20 {
					cost = 6
				}
			}
		} else {
			runeValue, size := utf8.DecodeRune(value[index:])
			width, cost = size, size
			if runeValue == utf8.RuneError && size == 1 || runeValue == '\u2028' || runeValue == '\u2029' {
				cost = 6
			}
		}
		if err := b.take(cost); err != nil {
			return err
		}
		index += width
	}
	return nil
}

type budgetCell struct {
	budget       *resultBudget
	databaseType string
	value        any
}

// Scanner 直接检查驱动值，避免 database/sql 扫入 *any 时先复制整个 []byte。
// 驱动可能已经分配了完整值；此处只约束应用累计保留/转换的结果。
func (cell *budgetCell) Scan(value any) error {
	// 只保留已知驱动标量。容器、指针、命名切片等可能引用无界 backing buffer，
	// 不能仅按可见长度计费后原样保留，也不调用未知驱动的自定义编码器。
	switch value.(type) {
	case nil, bool, int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64,
		float32, float64, string, []byte, time.Time:
	default:
		return errors.New("unsupported database driver value")
	}
	if bytes, ok := value.([]byte); ok {
		if len(bytes) > cell.budget.remaining {
			return resultTooLarge()
		}
		if isExactDecimalType(cell.databaseType) || utf8.Valid(bytes) {
			if err := cell.budget.bytesText(bytes); err != nil {
				return err
			}
			cell.value = string(bytes)
			return nil
		}
		if err := cell.budget.take(resultNodeCost + len("base64:") + base64.StdEncoding.EncodedLen(len(bytes))); err != nil {
			return err
		}
		cell.value = "base64:" + base64.StdEncoding.EncodeToString(bytes)
		return nil
	}
	value = normalizeDatabaseValue(value, cell.databaseType)
	if err := cell.budget.value(reflect.ValueOf(value), 0); err != nil {
		return err
	}
	if text, ok := value.(string); ok {
		// 小子串也不能保留驱动的大 backing buffer。
		value = strings.Clone(text)
	}
	cell.value = value
	return nil
}

func scanBudgetValues(rows interface{ Scan(...any) error }, count int, types []*sql.ColumnType, budget *resultBudget) ([]any, error) {
	if count > budget.remaining/resultNodeCost {
		return nil, resultTooLarge()
	}
	cells, targets := make([]budgetCell, count), make([]any, count)
	for index := range cells {
		cells[index].budget = budget
		if index < len(types) && types[index] != nil {
			cells[index].databaseType = types[index].DatabaseTypeName()
			if len(cells[index].databaseType) > MaxResultBytes {
				return nil, resultTooLarge()
			}
		}
		targets[index] = &cells[index]
	}
	if err := rows.Scan(targets...); err != nil {
		return nil, err
	}
	values := make([]any, count)
	for index := range cells {
		values[index] = cells[index].value
	}
	return values, nil
}

// 经预算核查后复制列列表和列名，避免保留驱动的额外切片容量/字符串缓冲区。
func ownResultColumns(columns []string) []string {
	owned := make([]string, len(columns))
	for index, name := range columns {
		owned[index] = strings.Clone(name)
	}
	return owned
}

// 非法标签会使 encoding/json 改用 Go 字段名；不让预检按另一个短名字计费。
func validResultJSONTag(name string) bool {
	for _, character := range name {
		if !strings.ContainsRune("!#$%&()*+-./:;<=>?@[]^_{|}~ ", character) && !unicode.IsLetter(character) && !unicode.IsDigit(character) {
			return false
		}
	}
	return true
}
