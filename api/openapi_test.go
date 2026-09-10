package api_test

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
)

func TestOpenAPIIncludesIdentityAndTenantRoutes(t *testing.T) {
	contents, err := os.ReadFile("openapi.json")
	if err != nil {
		t.Fatal(err)
	}
	var document struct {
		OpenAPI    string                     `json:"openapi"`
		Paths      map[string]json.RawMessage `json:"paths"`
		Components struct {
			SecuritySchemes map[string]json.RawMessage `json:"securitySchemes"`
		} `json:"components"`
	}
	if err := json.Unmarshal(contents, &document); err != nil {
		t.Fatalf("invalid OpenAPI JSON: %v", err)
	}
	wantPaths := []string{
		"/health/live", "/health/ready", "/api/v1/auth/registrations", "/api/v1/auth/sessions",
		"/api/v1/auth/social/{provider}/sessions", "/api/v1/auth/social/{provider}/callback",
		"/api/v1/auth/social/continue", "/api/v1/onboarding/stores", "/api/v1/store", "/api/v1/guest-sessions",
		"/api/v1/memberships", "/api/v1/memberships/{user_id}",
		"/api/v1/admin/catalog/categories", "/api/v1/admin/catalog/categories/{category_id}",
		"/api/v1/admin/catalog/categories/{category_id}/archive", "/api/v1/admin/catalog/products",
		"/api/v1/admin/catalog/products/{product_id}", "/api/v1/admin/catalog/products/{product_id}/archive",
	}
	for _, path := range wantPaths {
		if _, ok := document.Paths[path]; !ok {
			t.Errorf("OpenAPI path %q is missing", path)
		}
	}
	if document.OpenAPI != "3.1.0" || document.Components.SecuritySchemes["bearerAuth"] == nil || document.Components.SecuritySchemes["platformAdminBearer"] == nil {
		t.Fatalf("OpenAPI metadata or bearer security scheme is missing")
	}
}

func TestOpenAPIReferencesAndOperationContracts(t *testing.T) {
	contents, err := os.ReadFile("openapi.json")
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := json.Unmarshal(contents, &document); err != nil {
		t.Fatal(err)
	}
	validateReferences(t, document, document)
	assertOperationContract(t, document, "/api/v1/onboarding/stores", "post", true, []string{"201", "400", "401", "409", "429", "503"})
	assertOperationContract(t, document, "/api/v1/memberships", "post", true, []string{"201", "400", "401", "403", "409", "503"})
	assertOperationContract(t, document, "/api/v1/memberships/{user_id}", "patch", true, []string{"200", "400", "401", "403", "404", "409", "503"})
	assertOperationContract(t, document, "/api/v1/guest-sessions/current", "get", false, []string{"200", "401", "404", "503"})
	assertOperationContract(t, document, "/api/v1/auth/sessions/refresh", "post", true, []string{"200", "400", "401", "429", "503"})
	assertOperationContract(t, document, "/api/v1/admin/catalog/products", "get", false, []string{"200", "400", "401", "403", "404", "503"})
	assertOperationContract(t, document, "/api/v1/admin/catalog/products", "post", true, []string{"201", "400", "401", "403", "404", "409", "503"})
	assertOperationContract(t, document, "/api/v1/admin/catalog/products/{product_id}", "put", true, []string{"200", "400", "401", "403", "404", "409", "503"})
	assertOperationContract(t, document, "/api/v1/admin/catalog/categories/{category_id}/archive", "post", true, []string{"200", "400", "401", "403", "404", "409", "503"})
	assertSecurityScheme(t, document, "/api/v1/onboarding/stores", "post", "platformAdminBearer")
	assertSecurityScheme(t, document, "/api/v1/admin/catalog/products", "post", "bearerAuth")
	assertServer(t, document, "/api/v1/auth/social/{provider}/callback", "get", "https://auth.{baseDomain}")
	assertOnboardingHasNoSession(t, document)
	assertCatalogResponseSchemas(t, document)
}

func assertCatalogResponseSchemas(t *testing.T, document map[string]any) {
	t.Helper()
	schemas := document["components"].(map[string]any)["schemas"].(map[string]any)
	for _, name := range []string{"CatalogVariant", "CatalogMedia"} {
		schema := schemas[name].(map[string]any)
		if _, composed := schema["allOf"]; composed || schema["additionalProperties"] != false {
			t.Errorf("%s is not a closed standalone response schema", name)
		}
	}
	product := schemas["CatalogProduct"].(map[string]any)
	properties := product["properties"].(map[string]any)
	for _, name := range []string{"variants", "category_ids", "media"} {
		if properties[name].(map[string]any)["type"] != "array" {
			t.Errorf("CatalogProduct.%s is not an array", name)
		}
	}
}

func assertSecurityScheme(t *testing.T, document map[string]any, path, method, scheme string) {
	t.Helper()
	operation := document["paths"].(map[string]any)[path].(map[string]any)[method].(map[string]any)
	security := operation["security"].([]any)
	if len(security) != 1 || security[0].(map[string]any)[scheme] == nil {
		t.Errorf("%s %s does not require %s", method, path, scheme)
	}
}

func assertServer(t *testing.T, document map[string]any, path, method, want string) {
	t.Helper()
	operation := document["paths"].(map[string]any)[path].(map[string]any)[method].(map[string]any)
	servers := operation["servers"].([]any)
	if len(servers) != 1 || servers[0].(map[string]any)["url"] != want {
		t.Errorf("%s %s server does not match %s", method, path, want)
	}
}

func assertOnboardingHasNoSession(t *testing.T, document map[string]any) {
	t.Helper()
	operation := document["paths"].(map[string]any)["/api/v1/onboarding/stores"].(map[string]any)["post"].(map[string]any)
	created := operation["responses"].(map[string]any)["201"].(map[string]any)
	if created["$ref"] != "#/components/responses/OnboardingResult" {
		t.Fatalf("onboarding response = %#v", created)
	}
	encoded, err := json.Marshal(document["components"].(map[string]any)["responses"].(map[string]any)["OnboardingResult"])
	if err != nil {
		t.Fatal(err)
	}
	text := string(encoded)
	if strings.Contains(text, "access_token") || strings.Contains(text, "refresh_token") || !strings.Contains(text, "owner_user_id") {
		t.Fatalf("onboarding schema = %s", text)
	}
}

func validateReferences(t *testing.T, root map[string]any, value any) {
	t.Helper()
	switch typed := value.(type) {
	case map[string]any:
		if reference, ok := typed["$ref"].(string); ok {
			if _, err := resolveReference(root, reference); err != nil {
				t.Error(err)
			}
		}
		for _, child := range typed {
			validateReferences(t, root, child)
		}
	case []any:
		for _, child := range typed {
			validateReferences(t, root, child)
		}
	}
}

func resolveReference(root map[string]any, reference string) (any, error) {
	if !strings.HasPrefix(reference, "#/") {
		return nil, fmt.Errorf("unsupported OpenAPI reference %q", reference)
	}
	var current any = root
	for _, part := range strings.Split(strings.TrimPrefix(reference, "#/"), "/") {
		object, ok := current.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("invalid OpenAPI reference %q", reference)
		}
		current, ok = object[part]
		if !ok {
			return nil, fmt.Errorf("unresolved OpenAPI reference %q", reference)
		}
	}
	return current, nil
}

func assertOperationContract(t *testing.T, document map[string]any, path, method string, wantRequestBody bool, responseCodes []string) {
	t.Helper()
	paths := document["paths"].(map[string]any)
	operation := paths[path].(map[string]any)[method].(map[string]any)
	if _, ok := operation["operationId"].(string); !ok {
		t.Errorf("%s %s has no operationId", method, path)
	}
	_, hasRequestBody := operation["requestBody"]
	if hasRequestBody != wantRequestBody {
		t.Errorf("%s %s requestBody present = %v", method, path, hasRequestBody)
	}
	responses := operation["responses"].(map[string]any)
	for _, code := range responseCodes {
		if _, ok := responses[code]; !ok {
			t.Errorf("%s %s response %s is missing", method, path, code)
		}
	}
}
