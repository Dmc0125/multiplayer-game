package main

import (
	"slices"
	"testing"
)

func TestFilterMigrationsToExecute(t *testing.T) {
	{
		t.Log("migrate up, no migrations executed yet")

		direction := Up
		allMigrations := []int{1, 2, 3, 4, 5}
		lastExists := false
		last := 0
		toExecute := filterMigrationsToExecute(direction, allMigrations, lastExists, last)
		if len(toExecute) != 5 {
			t.Errorf("expected to execute all migrations, got %d", len(toExecute))
		}
		expected := []int{1, 2, 3, 4, 5}
		if !slices.Equal(toExecute, expected) {
			t.Errorf("expected %v, got %v", expected, toExecute)
		}
	}

	{
		t.Log("migrate down, no migrations executed yet")

		direction := Down
		allMigrations := []int{1, 2, 3, 4, 5}
		lastExists := false
		last := 0
		toExecute := filterMigrationsToExecute(direction, allMigrations, lastExists, last)
		if len(toExecute) != 0 {
			t.Errorf("expected to execute no migrations, got %d", len(toExecute))
		}
	}

	{
		t.Log("migrate up, last migration executed")

		direction := Up
		allMigrations := []int{1, 2, 3, 4, 5}
		lastExists := true
		last := 2
		toExecute := filterMigrationsToExecute(direction, allMigrations, lastExists, last)
		if len(toExecute) != 3 {
			t.Errorf("expected to execute last migration, got %d", len(toExecute))
		}
		expected := []int{3, 4, 5}
		if !slices.Equal(toExecute, expected) {
			t.Errorf("expected %v, got %v", expected, toExecute)
		}
	}

	{
		t.Log("migrate down, last migration executed")

		direction := Down
		allMigrations := []int{1, 2, 3, 4, 5}
		lastExists := true
		last := 3
		toExecute := filterMigrationsToExecute(direction, allMigrations, lastExists, last)
		if len(toExecute) != 3 {
			t.Errorf("expected to execute last migration, got %d", len(toExecute))
		}
		expected := []int{3, 2, 1}
		if !slices.Equal(toExecute, expected) {
			t.Errorf("expected %v, got %v", expected, toExecute)
		}
	}
}
