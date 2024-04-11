package test

import (
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/gin-gonic/gin"
	"github.com/sparkle-technologies/swagger_gin"
	"github.com/sparkle-technologies/swagger_gin/router"
	"github.com/sparkle-technologies/swagger_gin/swagger"
)

type TestRequest struct {
	Username string `json:"username" form:"username" query:"username"`
	Password string `json:"password" form:"password" query:"password"`
}

type TestResponse struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func Handler(c *gin.Context, req TestRequest) {
	var resp TestResponse
	resp.Code = 200
	resp.Message = "success"
	c.JSON(200, resp)
}
func newSwagger() *swagger.Swagger {

	return swagger.New(
		"Test swagger gin",
		"For test this package",
		"0.1.0",
		swagger.License(&openapi3.License{
			Name: "Apache License 2.0",
			URL:  "",
		}),
		swagger.Contact(&openapi3.Contact{
			Name:  "",
			URL:   "",
			Email: "",
		}),
		swagger.TermsOfService(""),
	)
}

func TestSwag(t *testing.T) {
	engine := swagger_gin.New(newSwagger())
	//engine.Use(...)
	engine.POST("/test1/test2/dsads", router.New(
		Handler,
		router.Responses(router.Response{"200": router.ResponseItem{
			Description: "Test api response",
			Model:       TestResponse{},
			Headers:     nil,
		}})))
	engine.Run(":8081")
}
