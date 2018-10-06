package typescriptify

import (
	"errors"
	"fmt"
	"io/ioutil"
	"os"
	"path"
	"reflect"
	"strings"
	"time"

	"github.com/tkrajina/go-reflector/reflector"
)

const (
	tsTransformTag = "ts_transform"
	tsType         = "ts_type"

	tsString  = "string"
	tsAny     = "any"
	tsBoolean = "boolean"
	tsNumber  = "number"
)

type TypeScriptify struct {
	Prefix           string
	Suffix           string
	Indent           string
	CreateFromMethod bool
	BackupDir        string // If empty no backup
	DontExport       bool

	golangTypes []*reflector.Obj
	types       map[reflect.Kind]string

	// throwaway, used when converting
	alreadyConverted map[reflect.Type]bool
}

func New() *TypeScriptify {
	result := new(TypeScriptify)
	result.Indent = "\t"
	result.BackupDir = "."

	types := make(map[reflect.Kind]string)

	types[reflect.Bool] = tsBoolean
	types[reflect.Interface] = tsAny

	types[reflect.Int] = tsNumber
	types[reflect.Int8] = tsNumber
	types[reflect.Int16] = tsNumber
	types[reflect.Int32] = tsNumber
	types[reflect.Int64] = tsNumber
	types[reflect.Uint] = tsNumber
	types[reflect.Uint8] = tsNumber
	types[reflect.Uint16] = tsNumber
	types[reflect.Uint32] = tsNumber
	types[reflect.Uint64] = tsNumber
	types[reflect.Float32] = tsNumber
	types[reflect.Float64] = tsNumber

	types[reflect.String] = tsString

	result.types = types

	result.Indent = "    "
	result.CreateFromMethod = true

	return result
}

func (t *TypeScriptify) Add(obj interface{}) {
	t.golangTypes = append(t.golangTypes, reflector.New(obj))
}

func (t *TypeScriptify) AddType(obj reflect.Type) {
	t.golangTypes = append(t.golangTypes, reflector.New(reflect.New(obj).Elem().Interface()))
}

func (t *TypeScriptify) Convert(customCode map[string]string) (string, error) {
	t.alreadyConverted = make(map[reflect.Type]bool)

	result := ""
	for _, obj := range t.golangTypes {
		typeScriptCode, err := t.convertType(obj, customCode)
		if err != nil {
			return "", err
		}
		result += "\n" + strings.Trim(typeScriptCode, " "+t.Indent+"\r\n")
	}
	return result, nil
}

func loadCustomCode(fileName string) (map[string]string, error) {
	result := make(map[string]string)
	f, err := os.Open(fileName)
	if err != nil {
		if os.IsNotExist(err) {
			return result, nil
		}
		return result, err
	}
	defer f.Close()

	bytes, err := ioutil.ReadAll(f)
	if err != nil {
		return result, err
	}

	var currentName string
	var currentValue string
	lines := strings.Split(string(bytes), "\n")
	for _, line := range lines {
		trimmedLine := strings.TrimSpace(line)
		if strings.HasPrefix(trimmedLine, "//[") && strings.HasSuffix(trimmedLine, ":]") {
			currentName = strings.Replace(strings.Replace(trimmedLine, "//[", "", -1), ":]", "", -1)
			currentValue = ""
		} else if trimmedLine == "//[end]" {
			result[currentName] = strings.TrimRight(currentValue, " \t\r\n")
			currentName = ""
			currentValue = ""
		} else if len(currentName) > 0 {
			currentValue += line + "\n"
		}
	}

	return result, nil
}

func (t TypeScriptify) backup(fileName string) error {
	fileIn, err := os.Open(fileName)
	if err != nil {
		if !os.IsNotExist(err) {
			return err
		}
		// No need to backup, just return:
		return nil
	}
	defer fileIn.Close()

	bytes, err := ioutil.ReadAll(fileIn)
	if err != nil {
		return err
	}

	_, backupFn := path.Split(fmt.Sprintf("%s-%s.backup", fileName, time.Now().Format("2006-01-02T15_04_05.99")))
	if t.BackupDir != "" {
		backupFn = path.Join(t.BackupDir, backupFn)
	}

	return ioutil.WriteFile(backupFn, bytes, os.FileMode(0700))
}

func (t TypeScriptify) ConvertToFile(fileName string) error {
	if len(t.BackupDir) > 0 {
		err := t.backup(fileName)
		if err != nil {
			return err
		}
	}

	customCode, err := loadCustomCode(fileName)
	if err != nil {
		return err
	}

	f, err := os.Create(fileName)
	if err != nil {
		return err
	}
	defer f.Close()

	converted, err := t.Convert(customCode)
	if err != nil {
		return err
	}

	f.WriteString("/* Do not change, this code is generated from Golang structs */\n\n")
	f.WriteString(converted)
	if err != nil {
		return err
	}

	return nil
}

func (t *TypeScriptify) convertType(obj *reflector.Obj, customCode map[string]string) (string, error) {
	if _, found := t.alreadyConverted[obj.Type()]; found { // Already converted
		return "", nil
	}
	t.alreadyConverted[obj.Type()] = true

	entityName := fmt.Sprintf("%s%s%s", t.Prefix, t.Suffix, obj.Type().Name())
	if entityName == "" {
		return "", errors.New("empty entity name")
	}
	result := []string{fmt.Sprintf("class %s {", entityName)}
	if !t.DontExport {
		result[0] = "export " + result[0]
	}
	builder := typeScriptClassBuilder{
		types:  t.types,
		indent: t.Indent,
	}

	for _, field := range obj.FieldsFlattened() {
		lines, err := t.convertTypeField(&builder, field, customCode)
		if err != nil {
			return "", err
		}
		result = append(lines, result...)
	}

	result = append(result, strings.TrimRight(builder.fields, "\n "))
	if t.CreateFromMethod {
		result = append(result, fmt.Sprintf("\n%sstatic createFrom(source: any) {", t.Indent))
		result = append(result, fmt.Sprintf("%s%sif ('string' === typeof source) source = JSON.parse(source);", t.Indent, t.Indent))
		result = append(result, fmt.Sprintf("%s%sconst result = new %s();", t.Indent, t.Indent, entityName))
		result = append(result, strings.TrimRight(builder.createFromMethodBody, "\n "))
		result = append(result, fmt.Sprintf("%s%sreturn result;", t.Indent, t.Indent))
		result = append(result, fmt.Sprintf("%s}\n", t.Indent))
	}

	if customCode != nil {
		code := customCode[entityName]
		result = append(result, t.Indent+"//["+entityName+":]\n"+code+"\n\n"+t.Indent+"//[end]")
	}

	result = append(result, "}")

	return strings.Join(result, "\n"), nil
}

func (t *TypeScriptify) parseJsonFieldNameFromTag(field reflector.ObjField) (string, error) {
	jsonTag, err := field.Tag("json")
	if err != nil {
		return "", err
	}
	jsonFieldName := ""
	if len(jsonTag) > 0 {
		jsonTagParts := strings.Split(jsonTag, ",")
		if len(jsonTagParts) > 0 {
			jsonFieldName = strings.Trim(jsonTagParts[0], t.Indent)
		}
	}
	return jsonFieldName, nil
}

func (t *TypeScriptify) convertTypeField(builder *typeScriptClassBuilder, field reflector.ObjField, customCode map[string]string) ([]string, error) {
	jsonFieldName, err := t.parseJsonFieldNameFromTag(field)
	if err != nil {
		return nil, err
	}

	var result []string
	if jsonFieldName == "" || jsonFieldName == "-" {
		return result, nil
	}

	var typeScriptChunk string

	customTransformation, err := field.Tag(tsTransformTag)
	if err != nil {
		return nil, err
	}
	if customTransformation != "" {
		err = builder.AddSimpleField(jsonFieldName, field)
	} else if field.Kind() == reflect.Ptr && field.Type().Elem().Kind() == reflect.Struct {
		typeScriptChunk, err = t.convertType(reflector.New(reflect.New(field.Type().Elem()).Elem().Interface()), customCode)
		if err != nil {
			return nil, err
		}
		builder.AddStructField(jsonFieldName, field.Name())
	} else if field.Kind() == reflect.Struct {
		typeScriptChunk, err = t.convertType(reflector.New(reflect.New(field.Type()).Elem().Interface()), customCode)
		if err != nil {
			return nil, err
		}
		builder.AddStructField(jsonFieldName, field.Name())
	} else if field.Kind() == reflect.Map {
		if field.Type().Key().Kind() != reflect.String {
			return nil, errors.New(fmt.Sprintf("map key must be string, found %s", field.Type().Name()))
		}
		if field.Type().Elem().Kind() == reflect.Struct { // Map with structs:
			typeScriptChunk, err = t.convertType(reflector.New(reflect.New(field.Type().Elem()).Elem().Interface()), customCode)
			if err != nil {
				return nil, err
			}
			builder.AddMapOfStructsField(jsonFieldName, field.Type().Elem().Name())
		} else { // Map with simple fields:
			err = builder.AddSimpleMapField(jsonFieldName, field.Type().Elem().Name(), field.Type().Elem().Kind())
		}
	} else if field.Kind() == reflect.Slice {
		if field.Type().Elem().Kind() == reflect.Struct { // Slice of structs:
			typeScriptChunk, err = t.convertType(reflector.New(reflect.New(field.Type().Elem()).Elem().Interface()), customCode)
			if err != nil {
				return nil, err
			}
			builder.AddArrayOfStructsField(jsonFieldName, field.Type().Elem().Name())
		} else { // Slice of simple fields:
			err = builder.AddSimpleArrayField(jsonFieldName, field.Type().Elem().Name(), field.Type().Elem().Kind())
		}
	} else { // Simple field:
		err = builder.AddSimpleField(jsonFieldName, field)
	}
	if err != nil {
		return nil, err
	}

	if typeScriptChunk != "" {
		result = append([]string{typeScriptChunk}, result...)
	}

	return result, nil
}

type typeScriptClassBuilder struct {
	types                map[reflect.Kind]string
	indent               string
	fields               string
	createFromMethodBody string
}

func (t *typeScriptClassBuilder) AddSimpleArrayField(fieldName, fieldType string, kind reflect.Kind) error {
	if typeScriptType, ok := t.types[kind]; ok {
		if len(fieldName) > 0 {
			t.fields += fmt.Sprintf("%s%s: %s[];\n", t.indent, fieldName, typeScriptType)
			t.createFromMethodBody += fmt.Sprintf("%s%sresult.%s = source['%s'];\n", t.indent, t.indent, fieldName, fieldName)
			return nil
		}
	}
	return errors.New(fmt.Sprintf("cannot find type for %s (%s/%s)", kind.String(), fieldName, fieldType))
}

func (t *typeScriptClassBuilder) AddSimpleField(fieldName string, field reflector.ObjField) error {
	fieldType, kind := field.Name(), field.Kind()
	customTSType, err := field.Tag(tsType)
	if err != nil {
		return err
	}

	typeScriptType := t.types[kind]
	if len(customTSType) > 0 {
		typeScriptType = customTSType
	}

	customTransformation, err := field.Tag(tsTransformTag)
	if err != nil {
		return err
	}

	if len(typeScriptType) > 0 && len(fieldName) > 0 {
		t.fields += fmt.Sprintf("%s%s: %s;\n", t.indent, fieldName, typeScriptType)
		if customTransformation == "" {
			t.createFromMethodBody += fmt.Sprintf("%s%sresult.%s = source['%s'];\n", t.indent, t.indent, fieldName, fieldName)
		} else {
			val := fmt.Sprintf(`source['%s']`, fieldName)
			expression := strings.Replace(customTransformation, "__VALUE__", val, -1)
			t.createFromMethodBody += fmt.Sprintf("%s%sresult.%s = %s;\n", t.indent, t.indent, fieldName, expression)
		}
		return nil
	}

	return errors.New("Cannot find type for " + fieldType + ", field: " + fieldName)
}

func (t *typeScriptClassBuilder) AddStructField(fieldName, fieldType string) {
	t.fields += fmt.Sprintf("%s%s: %s;\n", t.indent, fieldName, fieldType)
	t.createFromMethodBody += fmt.Sprintf("%s%sresult.%s = source['%s'] ? %s.createFrom(source['%s']) : null;\n", t.indent, t.indent, fieldName, fieldName, fieldType, fieldName)
}

func (t *typeScriptClassBuilder) AddArrayOfStructsField(fieldName, fieldType string) {
	t.fields += fmt.Sprintf("%s%s: %s[];\n", t.indent, fieldName, fieldType)
	t.createFromMethodBody += fmt.Sprintf("%s%sresult.%s = source['%s'] ? source['%s'].map(function(element) { return %s.createFrom(element); }) : null;\n",
		t.indent, t.indent, fieldName, fieldName, fieldName, fieldType)
}

func (t *typeScriptClassBuilder) AddMapOfStructsField(fieldName, fieldType string) {
	t.fields += fmt.Sprintf("%s%s: {[key: string]: %s};\n", t.indent, fieldName, fieldType)
	t.createFromMethodBody += fmt.Sprintf("%s%sif (source['%s']) {\n", t.indent, t.indent, fieldName)
	t.createFromMethodBody += fmt.Sprintf("%s%s%sresult.%s = {};\n", t.indent, t.indent, t.indent, fieldName)
	t.createFromMethodBody += fmt.Sprintf("%s%s%sfor (const key in source['%s']) result.%s[key] = %s.createFrom(source[key]);\n",
		t.indent, t.indent, t.indent, fieldName, fieldName, fieldType)
	t.createFromMethodBody += fmt.Sprintf("%s%s}\n", t.indent, t.indent)
}

func (t *typeScriptClassBuilder) AddSimpleMapField(fieldName, fieldType string, kind reflect.Kind) error {
	if typeScriptType, ok := t.types[kind]; ok {
		if len(fieldName) > 0 {
			t.fields += fmt.Sprintf("%s%s: {[key: string]: %s};\n", t.indent, fieldName, typeScriptType)
			t.createFromMethodBody += fmt.Sprintf("%s%sresult.%s = source['%s'];\n", t.indent, t.indent, fieldName, fieldName)
			return nil
		}
	}
	return errors.New(fmt.Sprintf("cannot find type for %s (%s/%s)", kind.String(), fieldName, fieldType))
}
