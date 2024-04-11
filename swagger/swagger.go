package swagger

import (
	"encoding/json"
	"fmt"
	"log"
	"mime/multipart"
	"net/http"
	"net/url"
	"reflect"
	"regexp"
	"strings"
	"time"

	"github.com/sparkle-technologies/swagger_gin/router"
	"github.com/sparkle-technologies/swagger_gin/security"

	"github.com/fatih/structtag"
	"github.com/getkin/kin-openapi/openapi3"
	"github.com/gin-gonic/gin"
	"github.com/gin-gonic/gin/binding"
	"github.com/invopop/yaml"
)

const (
	DEFAULT     = "default"
	VALIDATE    = "validate"
	DESCRIPTION = "description"
	QUERY       = "query"
	FORM        = "form"
	URI         = "uri"
	HEADER      = "header"
	COOKIE      = "cookie"
	JSON        = "json"
)

type Swagger struct {
	Title          string
	Description    string
	Version        string
	DocsUrl        string
	RedocUrl       string
	OpenAPIUrl     string
	Routers        map[*gin.RouterGroup]map[string]map[string]*router.Router
	Servers        openapi3.Servers
	TermsOfService string
	Contact        *openapi3.Contact
	License        *openapi3.License
	OpenAPI        *openapi3.T
	SwaggerOptions map[string]interface{}
	RedocOptions   map[string]interface{}
}

func New(title, description, version string, options ...Option) *Swagger {
	swagger := &Swagger{
		Title:       title,
		Description: description,
		Version:     version,
		DocsUrl:     "/docs",
		RedocUrl:    "/redoc",
		OpenAPIUrl:  "/openapi.json",
	}
	for _, option := range options {
		option(swagger)
	}
	return swagger
}

func (swagger *Swagger) getSecurityRequirements(
	securities []security.ISecurity,
) *openapi3.SecurityRequirements {
	securityRequirements := openapi3.NewSecurityRequirements()
	for _, s := range securities {
		provide := s.Provider()
		swagger.OpenAPI.Components.SecuritySchemes[provide] = &openapi3.SecuritySchemeRef{
			Value: s.Scheme(),
		}
		securityRequirements.With(openapi3.NewSecurityRequirement().Authenticate(provide))
	}
	return securityRequirements
}

func (swagger *Swagger) getBasicSchemaByType(typ reflect.Kind) *openapi3.Schema {
	var schema *openapi3.Schema
	var m = float64(0)
	switch typ {
	case reflect.Int, reflect.Int8, reflect.Int16:
		schema = openapi3.NewIntegerSchema()
	case reflect.Uint, reflect.Uint8, reflect.Uint16:
		schema = openapi3.NewIntegerSchema()
		schema.Min = &m
	case reflect.Int32:
		schema = openapi3.NewInt32Schema()
	case reflect.Uint32:
		schema = openapi3.NewInt32Schema()
		schema.Min = &m
	case reflect.Int64:
		schema = openapi3.NewInt64Schema()
	case reflect.Uint64:
		schema = openapi3.NewInt64Schema()
		schema.Min = &m
	case reflect.String:
		schema = openapi3.NewStringSchema()
	case reflect.Float32:
		schema = openapi3.NewFloat64Schema()
		schema.Format = "float"
	case reflect.Float64:
		schema = openapi3.NewFloat64Schema()
		schema.Format = "double"
	case reflect.Bool:
		schema = openapi3.NewBoolSchema()
	}
	return schema
}

func (swagger *Swagger) getSchemaByValue(
	t interface{},
	request bool,
) (ref string, schema *openapi3.Schema) {
	var m = float64(0)
	switch val := t.(type) {
	case int, int8, int16:
		schema = openapi3.NewIntegerSchema()
	case uint, uint8, uint16:
		schema = openapi3.NewIntegerSchema()
		schema.Min = &m
	case int32:
		schema = openapi3.NewInt32Schema()
	case uint32:
		schema = openapi3.NewInt32Schema()
		schema.Min = &m
	case int64:
		schema = openapi3.NewInt64Schema()
	case uint64:
		schema = openapi3.NewInt64Schema()
		schema.Min = &m
	case string:
		schema = openapi3.NewStringSchema()
	case time.Time:
		schema = openapi3.NewDateTimeSchema()
	case float32:
		schema = openapi3.NewFloat64Schema()
		schema.Format = "float"
	case float64:
		schema = openapi3.NewFloat64Schema()
		schema.Format = "double"
	case bool:
		schema = openapi3.NewBoolSchema()
	case []byte:
		schema = openapi3.NewBytesSchema()
	case *multipart.FileHeader:
		schema = openapi3.NewStringSchema()
		schema.Format = "binary"
	case []*multipart.FileHeader:
		schema = openapi3.NewArraySchema()
		schema.Items = &openapi3.SchemaRef{
			Value: &openapi3.Schema{
				Type:   &openapi3.Types{openapi3.TypeString},
				Format: "binary",
			},
		}
	case EnumAble:
		valEnums := val.Enums()
		typ_ := reflect.TypeOf(val)
		name := typ_.Name()
		enums := make([]interface{}, 0, len(valEnums))
		names := make([]string, 0, len(valEnums))
		valMap := make(map[interface{}]struct{}, len(valEnums))
		for k, v := range valEnums {
			enums = append(enums, v)
			names = append(names, k)
			valMap[v] = struct{}{}
		}

		ref = generateRefName(name)
		schema = NewEnumSchema(name, typ_.Kind())
		schema.Title = name
		schema.Enum = enums
		schema.Discriminator = &openapi3.Discriminator{
			Extensions: map[string]interface{}{
				"x-enum-varnames": names,
			},
		}

		if swagger.OpenAPI.Components.Schemas == nil {
			swagger.OpenAPI.Components.Schemas = make(openapi3.Schemas)
		}

		swagger.OpenAPI.Components.Schemas[name] = openapi3.NewSchemaRef("", schema)

		router.Validate.RegisterCustomTypeFunc(func(field reflect.Value) interface{} {
			val := field.Interface()
			if _, ok := valMap[val]; ok {
				return GetEnumVal(name, typ_.Kind(), val)
			}

			panic(fmt.Errorf("enum '%s' invalid value '%v'", typ_.Name(), val))
		}, enums...)

	default:
		if request {
			ref, schema = swagger.getRequestSchemaByModel(t)
		} else {
			ref, schema = swagger.getResponseSchemaByModel(t)
		}
	}
	return ref, schema
}

func (swagger *Swagger) getRequestSchemaByModel(model interface{}) (string, *openapi3.Schema) {
	ref := ""
	type_ := reflect.TypeOf(model)
	if type_ == nil {
		schema := openapi3.NewObjectSchema()
		schema.Items = &openapi3.SchemaRef{Value: openapi3.NewObjectSchema()}
		return ref, schema
	}
	value_ := reflect.ValueOf(model)
	schema := openapi3.NewObjectSchema()
	if type_.Kind() == reflect.Ptr {
		type_ = type_.Elem()
		if value_.IsNil() {
			value_ = reflect.New(type_)
		}
	}

	if value_.Kind() == reflect.Ptr {
		value_ = value_.Elem()
	}
	if type_.Kind() == reflect.Struct {
		for i := 0; i < type_.NumField(); i++ {
			field := type_.Field(i)
			fieldType := field.Type
			fieldvalue := value_.Field(i)
			tags, err := structtag.Parse(string(field.Tag))
			if err != nil {
				panic(err)
			}
			tag, err := tags.Get(FORM)
			if err != nil {
				continue
			}

			// dereference
			if fieldType.Kind() == reflect.Ptr {
				fieldType = fieldType.Elem()
				if fieldvalue.IsNil() {
					fieldvalue = reflect.New(fieldType)
				}
			}

			if fieldvalue.Kind() == reflect.Ptr {
				fieldvalue = fieldvalue.Elem()
			}

			fieldRef, fieldSchema := swagger.getSchemaByValue(fieldvalue.Interface(), true)
			descriptionTag, err := tags.Get(DESCRIPTION)
			if err == nil {
				fieldSchema.Description = descriptionTag.Name
			}

			defaultTag, err := tags.Get(DEFAULT)
			if err == nil {
				fieldSchema.Default = defaultTag.Name
			}
			schema.Properties[tag.Name] = openapi3.NewSchemaRef(fieldRef, fieldSchema)
		}
	} else if type_.Kind() == reflect.Slice {
		schema = openapi3.NewArraySchema()
		ref, refSchema := swagger.getRequestSchemaByModel(reflect.New(type_.Elem()).Elem().Interface())
		schema.Items = &openapi3.SchemaRef{Ref: ref, Value: refSchema}
	} else if type_.Kind() == reflect.Map {
		schema = openapi3.NewObjectSchema()
		ref, refSchema := swagger.getRequestSchemaByModel(reflect.New(type_.Elem()).Elem().Interface())
		schema.Items = &openapi3.SchemaRef{Ref: ref, Value: refSchema}
	} else {
		ref, schema = swagger.getSchemaByValue(model, true)
	}
	return ref, schema
}

func (swagger *Swagger) getComponentByModel(model any, isRequest bool) {
	type_ := reflect.TypeOf(model)
	if type_ == nil {
		//swagger.OpenAPI.Components.Schemas[schemaRef.Value.Title] = schemaRef
		//return &openapi3.SchemaRef{Value: openapi3.NewObjectSchema()}
		// TODO ?
		return
	}
	value_ := reflect.ValueOf(model)

	// dereference
	if type_.Kind() == reflect.Ptr {
		type_ = type_.Elem()
		if value_.IsNil() {
			value_ = reflect.New(type_)
		}
	}
	if value_.Kind() == reflect.Ptr {
		value_ = value_.Elem()
	}

	// openapi3.Schemas k -> struct name = title -> struct name
	// get struct name from request.SchemaName
	schemaRef := &openapi3.SchemaRef{}
	schemaRef.Value = openapi3.NewObjectSchema()

	// schemaRef is the outer field
	// if it is a struct, handle its fields
	if type_.Kind() == reflect.Struct {
		for i := 0; i < type_.NumField(); i++ {
			field := type_.Field(i)
			fieldType := field.Type
			fieldvalue := value_.Field(i)
			tags, err := structtag.Parse(string(field.Tag))
			if err != nil {
				panic(err)
			}
			tag, err := tags.Get(FORM)
			if err != nil && isRequest {
				// only request body need to be added to components
				continue
			}

			// dereference
			if fieldType.Kind() == reflect.Ptr {
				fieldType = fieldType.Elem()
				if fieldvalue.IsNil() {
					fieldvalue = reflect.New(fieldType)
				}
			}

			if fieldvalue.Kind() == reflect.Ptr {
				fieldvalue = fieldvalue.Elem()
			}

			var fieldName = field.Name
			if isRequest {
				fieldName = tag.Name
			} else if jsonTag, err := tags.Get(JSON); err == nil && jsonTag != nil {
				fieldName = jsonTag.Name
			}

			if isRequiredTags(tags) {
				schemaRef.Value.Required = append(schemaRef.Value.Required, fieldName)
			}

			if fieldType.Kind() == reflect.Struct {
				if fieldType.Name() == "Time" {
					fieldRef, fieldSchema := swagger.getSchemaByValue(
						fieldvalue.Interface(),
						isRequest,
					)
					fieldSchema.Format = "date-time"
					fieldSchema.Type = &openapi3.Types{openapi3.TypeString}

					schemaRef.Value.Properties[fieldName] = openapi3.NewSchemaRef(
						fieldRef,
						fieldSchema,
					)
					continue
				} else if !swagger.checkSchemaExist(fieldType.Name()) {
					swagger.getComponentByModel(reflect.New(fieldType).Elem().Interface(), isRequest)
				}
				//schemaRef.Ref = generateRefName(field.Type.Name())
				fieldSchemaRef := openapi3.NewSchemaRef(generateRefName(fieldType.Name()), nil)
				schemaRef.Value.Properties[fieldName] = fieldSchemaRef
			} else if fieldType.Kind() == reflect.Slice {
				// check if type.Elem() if built-in type
				fieldRef, fieldSchema := swagger.getSchemaByValue(fieldvalue.Interface(), isRequest)
				if !isBuiltinType(fieldType.Elem()) {
					subFieldValue := reflect.New(fieldType.Elem()).Elem().Interface()
					subFieldType := reflect.TypeOf(subFieldValue)
					if subFieldType.Kind() == reflect.Ptr {
						subFieldType = subFieldType.Elem()

					}

					if !swagger.checkSchemaExist(subFieldType.Name()) {
						swagger.getComponentByModel(subFieldValue, isRequest)
					}

					fieldSchemaRef := openapi3.NewSchemaRef(generateRefName(subFieldType.Name()), nil)
					fieldSchema.Items = fieldSchemaRef
				} else {
					descriptionTag, err := tags.Get(DESCRIPTION)
					if err == nil {
						fieldSchema.Description = descriptionTag.Name
					}

					defaultTag, err := tags.Get(DEFAULT)
					if err == nil {
						fieldSchema.Default = defaultTag.Name
					}
				}
				schemaRef.Value.Properties[fieldName] = openapi3.NewSchemaRef(fieldRef, fieldSchema)
			} else if fieldType.Kind() == reflect.Map {
				// To define a dictionary, use type: object and use the additionalProperties
				// keyword to specify the type of values in key/value pairs.
				// the keys must be string

				// get the value type
				mapValueType := fieldType.Elem()
				fieldSchema := openapi3.NewObjectSchema()

				var ap openapi3.AdditionalProperties

				if mapValueType.Kind() == reflect.Interface {
					var b = true
					ap.Has = &b
				} else if mapValueType.Kind() == reflect.Struct {
					if !swagger.checkSchemaExist(fieldType.Elem().Name()) {
						swagger.getComponentByModel(reflect.New(fieldType.Elem()).Elem().Interface(), isRequest)
					}
					ap.Schema = openapi3.NewSchemaRef(generateRefName(fieldType.Elem().Name()), nil)
				} else {
					// basic type
					schema := swagger.getBasicSchemaByType(mapValueType.Kind())
					ap.Schema = openapi3.NewSchemaRef("", schema)
				}
				fieldSchema.AdditionalProperties = ap
				schemaRef.Value.Properties[fieldName] = openapi3.NewSchemaRef("", fieldSchema)
			} else if fieldType.Kind() == reflect.Interface {
				schemaRef.Value.Properties[fieldName] = openapi3.NewSchemaRef("", openapi3.NewObjectSchema())
			} else {

				// getSchemaByValue can't distinguish any with map[string]any and []any
				// all of them are nil in func
				fieldRef, fieldSchema := swagger.getSchemaByValue(fieldvalue.Interface(), isRequest)

				descriptionTag, err := tags.Get(DESCRIPTION)
				if err == nil {
					fieldSchema.Description = descriptionTag.Name
				}

				defaultTag, err := tags.Get(DEFAULT)
				if err == nil {
					fieldSchema.Default = defaultTag.Name
				}
				schemaRef.Value.Properties[fieldName] = openapi3.NewSchemaRef(fieldRef, fieldSchema)
			}
		}
	}

	if swagger.OpenAPI.Components.Schemas == nil {
		swagger.OpenAPI.Components.Schemas = make(openapi3.Schemas)
	}

	schemaRef.Value.Title = removePackageName(type_.Name())
	// if it goes here, the schemaRef has `Value` rather than `Ref`
	swagger.OpenAPI.Components.Schemas[schemaRef.Value.Title] = schemaRef
}

func (swagger *Swagger) getRequestBodyRef(
	name string,
	contentType string,
) *openapi3.RequestBodyRef {
	body := &openapi3.RequestBodyRef{
		Value: openapi3.NewRequestBody(),
	}
	body.Value.Required = true
	if contentType == "" {
		contentType = binding.MIMEJSON
	}
	schemaRef := openapi3.NewSchemaRef(generateRefName(removePackageName(name)), nil)
	body.Value.Content = openapi3.NewContent()
	body.Value.Content[contentType] = openapi3.NewMediaType().WithSchemaRef(schemaRef)
	return body
}

func (swagger *Swagger) getResponseSchemaByModel(model interface{}) (string, *openapi3.Schema) {
	ref := ""
	type_ := reflect.TypeOf(model)
	if type_ == nil {
		schema := openapi3.NewObjectSchema()
		schema.Items = &openapi3.SchemaRef{Value: openapi3.NewObjectSchema()}
		return ref, schema
	}
	value_ := reflect.ValueOf(model)
	if type_.Kind() == reflect.Ptr {
		type_ = type_.Elem()
		if value_.IsNil() {
			value_ = reflect.New(type_)
		}
	}

	if value_.Kind() == reflect.Ptr {
		value_ = value_.Elem()
	}
	schema := openapi3.NewObjectSchema()
	if type_.Kind() == reflect.Struct {
		ref = generateRefName(type_.Name())
		for i := 0; i < value_.NumField(); i++ {
			fieldValue := value_.Field(i)
			fieldType := value_.Type().Field(i)
			if fieldType.IsExported() && value_.IsValid() {
				fieldRef, fieldSchema := swagger.getSchemaByValue(fieldValue.Interface(), false)
				tags, err := structtag.Parse(string(fieldType.Tag))
				if err != nil {
					panic(err)
				}
				tag, err := tags.Get("json")
				if err != nil {
					continue
				}

				if isRequiredTags(tags) {
					schema.Required = append(schema.Required, tag.Name)
				}

				descriptionTag, err := tags.Get(DESCRIPTION)
				if err == nil {
					fieldSchema.Description = descriptionTag.Name
				}
				defaultTag, err := tags.Get(DEFAULT)
				if err == nil {
					fieldSchema.Default = defaultTag.Name
				}
				schema.Properties[tag.Name] = openapi3.NewSchemaRef(fieldRef, fieldSchema)
			}
		}
	} else if type_.Kind() == reflect.Slice {
		schema = openapi3.NewArraySchema()
		ref, schemaRef := swagger.getResponseSchemaByModel(reflect.New(type_.Elem()).Elem().Interface())
		schema.Items = &openapi3.SchemaRef{Ref: ref, Value: schemaRef}
	} else if type_.Kind() == reflect.Map {
		schema = openapi3.NewObjectSchema()
		ref, schemaRef := swagger.getResponseSchemaByModel(reflect.New(type_.Elem()).Elem().Interface())
		schema.Items = &openapi3.SchemaRef{Ref: ref, Value: schemaRef}
	} else {
		ref, schema = swagger.getSchemaByValue(value_.Interface(), false)
	}
	return ref, schema
}

func (swagger *Swagger) getResponsesRef(
	response router.Response,
	contentType string,
) *openapi3.Responses {
	ret := openapi3.NewResponses()
	for k, v := range response {
		type_ := reflect.TypeOf(v.Model)
		if type_ == nil {
			continue
		}

		schemaRef := openapi3.NewSchemaRef(generateRefName(removePackageName(type_.Name())), nil)

		var content = make(openapi3.Content)
		if contentType == "" {
			contentType = binding.MIMEJSON
		}
		content[contentType] = openapi3.NewMediaType().WithSchemaRef(schemaRef)

		description := v.Description
		ret.Set(k, &openapi3.ResponseRef{
			Value: &openapi3.Response{
				Description: &description,
				Content:     content,
				Headers:     v.Headers,
			},
		})
	}

	return ret
}

func (swagger *Swagger) getParametersByModel(model interface{}) openapi3.Parameters {
	parameters := openapi3.NewParameters()
	if model == nil {
		return parameters
	}
	type_ := reflect.TypeOf(model)
	value_ := reflect.ValueOf(model)
	if type_.Kind() == reflect.Ptr {
		type_ = type_.Elem()
		if value_.IsNil() {
			value_ = reflect.New(type_)
		}
	}

	if value_.Kind() == reflect.Ptr {
		value_ = value_.Elem()
	}
	for i := 0; i < type_.NumField(); i++ {
		field := type_.Field(i)
		value := value_.Field(i)
		tags, err := structtag.Parse(string(field.Tag))
		if err != nil {
			panic(err)
		}
		parameter := &openapi3.Parameter{}
		queryTag, err := tags.Get(QUERY)
		if err == nil {
			parameter.In = openapi3.ParameterInQuery
			parameter.Name = queryTag.Name
		}
		uriTag, err := tags.Get(URI)
		if err == nil {
			parameter.In = openapi3.ParameterInPath
			parameter.Name = uriTag.Name
		}
		headerTag, err := tags.Get(HEADER)
		if err == nil {
			parameter.In = openapi3.ParameterInHeader
			parameter.Name = headerTag.Name
		}
		cookieTag, err := tags.Get(COOKIE)
		if err == nil {
			parameter.In = openapi3.ParameterInCookie
			parameter.Name = cookieTag.Name
		}
		if parameter.In == "" {
			continue
		}
		descriptionTag, err := tags.Get(DESCRIPTION)
		if err == nil {
			parameter.Description = descriptionTag.Name
		}

		if isRequiredTags(tags) {
			parameter.Required = true
		}

		defaultTag, err := tags.Get(DEFAULT)
		ref, schema := swagger.getSchemaByValue(value.Interface(), true)
		if err == nil {
			schema.Default = defaultTag.Name
		}
		parameter.Schema = &openapi3.SchemaRef{
			Value: schema,
		}
		parameters = append(parameters, &openapi3.ParameterRef{
			Ref:   ref,
			Value: parameter,
		})
	}
	return parameters
}

// /:id -> /{id}
func (swagger *Swagger) fixPath(path string) string {
	reg := regexp.MustCompile("/:([0-9a-zA-Z]+)")
	return reg.ReplaceAllString(path, "/{${1}}")
}

func (swagger *Swagger) getPaths() *openapi3.Paths {
	paths := openapi3.NewPaths()
	for group, routers := range swagger.Routers {
		for path, m := range routers {
			path, err := url.JoinPath(group.BasePath(), path)
			if err != nil {
				log.Panicln(err)
			}

			pathItem := &openapi3.PathItem{}
			for method, r := range m {
				// r -> router
				// handle request here
				if r.Exclude {
					continue
				}

				swagger.getComponentByModel(r.Model, true)
				for _, resp := range r.Response {
					swagger.getComponentByModel(resp.Model, false)
				}

				model := r.Model
				operation := &openapi3.Operation{
					Tags:        r.Tags,
					OperationID: r.OperationID,
					Summary:     r.Summary,
					Description: r.Description,
					Deprecated:  r.Deprecated,
					Responses:   swagger.getResponsesRef(r.Response, r.ResponseContentType),
					Parameters:  swagger.getParametersByModel(model),
					Security:    swagger.getSecurityRequirements(r.Securities),
				}

				var requestBody *openapi3.RequestBodyRef
				reqType := reflect.TypeOf(r.Model)
				if reqType != nil {
					requestBody = swagger.getRequestBodyRef(
						reflect.TypeOf(r.Model).Name(),
						r.RequestContentType,
					)
				}

				if method == http.MethodGet {
					pathItem.Get = operation
				} else if method == http.MethodPost {
					pathItem.Post = operation
					operation.RequestBody = requestBody
				} else if method == http.MethodDelete {
					pathItem.Delete = operation
				} else if method == http.MethodPut {
					pathItem.Put = operation
					operation.RequestBody = requestBody
				} else if method == http.MethodPatch {
					pathItem.Patch = operation
				} else if method == http.MethodHead {
					pathItem.Head = operation
				} else if method == http.MethodOptions {
					pathItem.Options = operation
				} else if method == http.MethodConnect {
					pathItem.Connect = operation
				} else if method == http.MethodTrace {
					pathItem.Trace = operation
				}
			}
			paths.Set(swagger.fixPath(path), pathItem)
		}
	}

	return paths
}

func (swagger *Swagger) BuildOpenAPI() {
	components := openapi3.NewComponents()
	components.SecuritySchemes = openapi3.SecuritySchemes{}
	swagger.OpenAPI = &openapi3.T{
		OpenAPI: "3.0.0",
		Info: &openapi3.Info{
			Title:          swagger.Title,
			Description:    swagger.Description,
			TermsOfService: swagger.TermsOfService,
			Contact:        swagger.Contact,
			License:        swagger.License,
			Version:        swagger.Version,
		},
		Servers:    swagger.Servers,
		Components: &components,
	}
	swagger.OpenAPI.Paths = swagger.getPaths()
}

func (swagger *Swagger) MarshalJSON() ([]byte, error) {
	return swagger.OpenAPI.MarshalJSON()
}

func (swagger *Swagger) MarshalYAML() ([]byte, error) {
	b, err := swagger.OpenAPI.MarshalJSON()
	if err != nil {
		return nil, err
	}
	var data interface{}
	if err = json.Unmarshal(b, &data); err != nil {
		return nil, err
	}
	return yaml.Marshal(data)
}

func (swagger *Swagger) WithDocsUrl(url string) *Swagger {
	DocsUrl(url)(swagger)
	return swagger
}

func (swagger *Swagger) WithRedocUrl(url string) *Swagger {
	RedocUrl(url)(swagger)
	return swagger
}

func (swagger *Swagger) WithTitle(title string) *Swagger {
	Title(title)(swagger)
	return swagger
}

func (swagger *Swagger) WithDescription(description string) *Swagger {
	Description(description)(swagger)
	return swagger
}

func (swagger *Swagger) WithVersion(version string) *Swagger {
	Version(version)(swagger)
	return swagger
}

func (swagger *Swagger) WithOpenAPIUrl(url string) *Swagger {
	OpenAPIUrl(url)(swagger)
	return swagger
}

func (swagger *Swagger) WithTermsOfService(termsOfService string) *Swagger {
	TermsOfService(termsOfService)(swagger)
	return swagger
}

func (swagger *Swagger) WithContact(contact *openapi3.Contact) *Swagger {
	Contact(contact)(swagger)
	return swagger
}

func (swagger *Swagger) WithLicense(license *openapi3.License) *Swagger {
	License(license)(swagger)
	return swagger
}

func (swagger *Swagger) WithServers(servers []*openapi3.Server) *Swagger {
	Servers(servers)(swagger)
	return swagger
}

func (swagger *Swagger) WithSwaggerOptions(options map[string]interface{}) *Swagger {
	SwaggerOptions(options)(swagger)
	return swagger
}

func (swagger *Swagger) WithRedocOptions(options map[string]interface{}) *Swagger {
	RedocOptions(options)(swagger)
	return swagger
}

func (swagger *Swagger) checkSchemaExist(name string) bool {
	for _, schema := range swagger.OpenAPI.Components.Schemas {
		if schema.Value != nil && schema.Value.Title == name {
			return true
		}
	}

	return false
}

func generateRefName(structName string) string {
	return "#/components/schemas/" + structName
}

func removePackageName(name string) string {
	if !strings.HasSuffix(name, "]") {
		split := strings.Split(name, ".")
		return split[len(split)-1]
	}

	structSplit := strings.Split(name, ".")
	templatesplit := strings.Split(name, "[")
	return strings.TrimSuffix(structSplit[len(structSplit)-1], "]") + templatesplit[0]
}

func isBuiltinType(t reflect.Type) bool {
	switch t.Kind() {
	case reflect.Bool, reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr,
		reflect.Float32, reflect.Float64, reflect.Complex64, reflect.Complex128, reflect.String,
		reflect.UnsafePointer:
		return true
	default:
		return false
	}
}

func isRequiredTags(tags *structtag.Tags) bool {
	validateTag, err := tags.Get(VALIDATE)
	if err == nil {
		names := strings.Split(validateTag.Name, ",")
		for _, name := range names {
			if name == "required" {
				return true
			}
		}
	}

	return false
}
