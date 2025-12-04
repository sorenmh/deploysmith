package models

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/go-playground/validator/v10"
)

// ValidationError represents a validation error with field context
type ValidationError struct {
	Field   string `json:"field"`
	Message string `json:"message"`
	Value   string `json:"value,omitempty"`
}

// Error implements the error interface
func (ve ValidationError) Error() string {
	if ve.Value != "" {
		return fmt.Sprintf("%s: %s (value: %q)", ve.Field, ve.Message, ve.Value)
	}
	return fmt.Sprintf("%s: %s", ve.Field, ve.Message)
}

// ValidationErrors represents multiple validation errors
type ValidationErrors []ValidationError

// Error implements the error interface
func (ves ValidationErrors) Error() string {
	if len(ves) == 0 {
		return ""
	}
	if len(ves) == 1 {
		return ves[0].Error()
	}

	var messages []string
	for _, ve := range ves {
		messages = append(messages, ve.Error())
	}
	return fmt.Sprintf("multiple validation errors: %s", strings.Join(messages, "; "))
}

// NewValidator creates a new validator with custom validation rules
func NewValidator() *validator.Validate {
	v := validator.New()

	// Register custom validation functions
	v.RegisterValidation("dns1123", validateDNS1123)
	v.RegisterValidation("image_ref", validateImageRef)
	v.RegisterValidation("env_var_name", validateEnvVarName)
	v.RegisterValidation("secret_ref", validateSecretRef)
	v.RegisterValidation("k8s_quantity", validateK8sQuantity)

	return v
}

// ValidateServiceDefinition validates a service definition with detailed error messages
func ValidateServiceDefinition(sd *ServiceDefinition) error {
	// First, run the struct-level validation
	if err := sd.Validate(); err != nil {
		return err
	}

	// Then run tag-based validation
	validator := NewValidator()
	if err := validator.Struct(sd); err != nil {
		return convertValidatorErrors(err)
	}

	return nil
}

// convertValidatorErrors converts go-playground validator errors to our custom format
func convertValidatorErrors(err error) error {
	if validationErrors, ok := err.(validator.ValidationErrors); ok {
		var errors ValidationErrors

		for _, ve := range validationErrors {
			errors = append(errors, ValidationError{
				Field:   ve.Field(),
				Message: getValidationMessage(ve),
				Value:   fmt.Sprintf("%v", ve.Value()),
			})
		}

		return errors
	}

	return err
}

// getValidationMessage returns a human-readable message for validation errors
func getValidationMessage(ve validator.FieldError) string {
	switch ve.Tag() {
	case "required":
		return "is required"
	case "min":
		return fmt.Sprintf("must be at least %s", ve.Param())
	case "max":
		return fmt.Sprintf("must be at most %s", ve.Param())
	case "dns1123":
		return "must be a valid DNS-1123 subdomain name (lowercase alphanumeric and hyphens)"
	case "image_ref":
		return "must be a valid container image reference (repository:tag)"
	case "env_var_name":
		return "must be a valid environment variable name (alphanumeric and underscores, starting with letter/underscore)"
	case "secret_ref":
		return "must be in format 'secret-name/key'"
	case "k8s_quantity":
		return "must be a valid Kubernetes resource quantity (e.g., '100m', '256Mi')"
	default:
		return ve.Error()
	}
}

// Custom validation functions

// validateDNS1123 validates DNS-1123 subdomain names
func validateDNS1123(fl validator.FieldLevel) bool {
	value := fl.Field().String()
	return isValidDNSName(value)
}

// validateImageRef validates container image references
func validateImageRef(fl validator.FieldLevel) bool {
	value := fl.Field().String()
	if value == "" {
		return false
	}

	// Basic pattern: must contain ':' and have valid characters
	pattern := `^[a-zA-Z0-9._/-]+:[a-zA-Z0-9._-]+$`
	matched, _ := regexp.MatchString(pattern, value)
	return matched
}

// validateEnvVarName validates environment variable names
func validateEnvVarName(fl validator.FieldLevel) bool {
	value := fl.Field().String()
	if value == "" {
		return false
	}

	// Must start with letter or underscore, followed by alphanumeric or underscores
	pattern := `^[a-zA-Z_][a-zA-Z0-9_]*$`
	matched, _ := regexp.MatchString(pattern, value)
	return matched
}

// validateSecretRef validates secret references in format "secret-name/key"
func validateSecretRef(fl validator.FieldLevel) bool {
	value := fl.Field().String()
	if value == "" {
		return true // Optional field
	}

	parts := strings.Split(value, "/")
	if len(parts) != 2 {
		return false
	}

	secretName := parts[0]
	keyName := parts[1]

	// Both parts must be valid DNS-1123 names
	return isValidDNSName(secretName) && isValidK8sKey(keyName)
}

// validateK8sQuantity validates Kubernetes resource quantities
func validateK8sQuantity(fl validator.FieldLevel) bool {
	value := fl.Field().String()
	if value == "" {
		return true // Optional field
	}

	// Pattern for Kubernetes quantities (e.g., "100m", "1Gi", "500")
	patterns := []string{
		`^\d+$`,                    // Raw number (e.g., "1")
		`^\d+m$`,                   // Millicores (e.g., "100m")
		`^\d+[KMGT]i?$`,           // Memory with binary/decimal units (e.g., "256Mi", "1Gi")
		`^\d+\.?\d*[KMGT]i?$`,     // Decimal quantities (e.g., "1.5Gi")
	}

	for _, pattern := range patterns {
		if matched, _ := regexp.MatchString(pattern, value); matched {
			return true
		}
	}

	return false
}

// isValidK8sKey checks if a string is a valid Kubernetes key name
func isValidK8sKey(key string) bool {
	if len(key) == 0 || len(key) > 253 {
		return false
	}

	// Allow alphanumeric, hyphens, underscores, and dots
	pattern := `^[a-zA-Z0-9._-]+$`
	matched, _ := regexp.MatchString(pattern, key)
	return matched
}

// ValidateComponentType validates if a component type is supported
func ValidateComponentType(componentType string) error {
	ct := ComponentType(componentType)
	if !ct.IsValid() {
		return fmt.Errorf("unsupported component type: %q (supported: %s, %s)",
			componentType, ComponentTypeWeb, ComponentTypeAPI)
	}
	return nil
}

// ValidateCronExpression validates cron expressions (for future cronjob support)
func ValidateCronExpression(expr string) error {
	if expr == "" {
		return fmt.Errorf("cron expression cannot be empty")
	}

	// Basic validation - must have 5 or 6 fields
	fields := strings.Fields(expr)
	if len(fields) != 5 && len(fields) != 6 {
		return fmt.Errorf("cron expression must have 5 or 6 fields, got %d", len(fields))
	}

	// For now, just check basic structure
	// TODO: Add more comprehensive cron validation when cronjobs are implemented
	return nil
}

// ValidateResourceQuantity validates and normalizes resource quantities
func ValidateResourceQuantity(quantity string) error {
	if quantity == "" {
		return nil
	}

	// Try to parse as integer first
	if _, err := strconv.Atoi(quantity); err == nil {
		return nil
	}

	// Check for valid patterns
	patterns := []string{
		`^\d+m$`,                 // millicores
		`^\d+(\.\d+)?[KMGT]i?$`, // memory
	}

	for _, pattern := range patterns {
		if matched, _ := regexp.MatchString(pattern, quantity); matched {
			return nil
		}
	}

	return fmt.Errorf("invalid resource quantity: %q", quantity)
}