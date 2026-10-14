package main

import (
	"reflect"
	"testing"
)

func TestCatalogOwnershipDoesNotIncludeIAMOrForeignObjects(t *testing.T) {
	apps, permissions, err := desiredCatalogObjects([]byte(`{"applications":[{"owner":"admin","name":"app-anas-catalog-db"},{"owner":"other","name":"app-anas-catalog-foreign"},{"owner":"admin","name":"app-anas-nextcloud"},{"owner":"admin","name":"app-built-in"}],"permissions":[{"owner":"anas","name":"permission-app-anas-catalog-db"},{"owner":"other","name":"permission-app-anas-catalog-foreign"}]}`))
	if err != nil || !reflect.DeepEqual(apps, []string{"app-anas-catalog-db"}) || !reflect.DeepEqual(permissions, []string{"permission-app-anas-catalog-db"}) {
		t.Fatalf("%v %v %v", apps, permissions, err)
	}
	if _, _, err := desiredCatalogObjects([]byte("{")); err == nil {
		t.Fatal("malformed input accepted")
	}
}
