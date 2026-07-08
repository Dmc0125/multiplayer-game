package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
)

const DB_CONN_STRING = "postgres://user:pwd@localhost:5432/game?sslmode=disable"

type Direction string

const (
	Up   Direction = "up"
	Down Direction = "down"
)

const createMigrationsTable = `
create table migrations (
	id int not null primary key,
	last int
)
`

func filterMigrationsToExecute(direction Direction, allMigrations []int, lastExists bool, last int) (toExecute []int) {
	if !lastExists {
		if direction == Down {
			return
		} else {
			toExecute = allMigrations
		}
	} else {
		if direction == Up {
			for _, num := range allMigrations {
				if num > last {
					toExecute = append(toExecute, num)
				}
			}
		} else {
			for i := len(allMigrations) - 1; i >= 0; i-- {
				num := allMigrations[i]
				if num <= last {
					toExecute = append(toExecute, num)
				}
			}
		}
	}
	return
}

const ansiReset = "\033[0m"
const ansiRed = "\033[0;31m"
const ansiGreen = "\033[0;32m"
const ansiBlue = "\033[0;34m"

func info(msg string) {
	fmt.Printf("%s==>%s %s\n", ansiBlue, ansiReset, msg)
}

func success(msg string) {
	fmt.Printf("%s✓✓✓%s %s\n", ansiGreen, ansiReset, msg)
}

func pError(msg string) {
	fmt.Printf("%s✗✗✗%s %s\n", ansiRed, ansiReset, msg)
	os.Exit(1)
}

func main() {
	args := os.Args[1:]

	if len(args) < 3 {
		fmt.Println("Usage: go run main.go <migrations_directory> <direction (up/down)> <count>")
		os.Exit(1)
	}

	migrationsDirectory := filepath.Clean(args[0])

	direction := Direction(args[1])
	if direction != Up && direction != Down {
		pError("direction must be up or down")
	}

	count, err := strconv.Atoi(args[2])
	if err != nil {
		pError(fmt.Sprintf("count must be an integer: %s", err))
	}

	info(fmt.Sprintf("Migrating %s %d migrations", direction, count))
	info("Reading migrations...")

	entries, err := os.ReadDir(migrationsDirectory)
	if err != nil {
		pError(fmt.Sprintf("unable to read migrations directory: %s", err))
	}

	var migrationsFiles []int
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		name := entry.Name()

		// trim prefix
		n := strings.TrimPrefix(name, fmt.Sprintf("%s_", direction))
		if n == name {
			continue
		}
		name = n

		// trim suffix
		n = strings.TrimSuffix(name, ".sql")
		if n == name {
			continue
		}
		name = n

		// number
		num, err := strconv.Atoi(name)
		if err != nil {
			continue
		}

		migrationsFiles = append(migrationsFiles, num)
	}
	slices.Sort(migrationsFiles)

	info("Connecting to database...")
	dbConn, err := pgx.Connect(context.Background(), DB_CONN_STRING)
	if err != nil {
		pError(fmt.Sprintf("unable to connect to database: %s", err))
	}

	info("Checking if migrations table exists...")

	insertMigrationsTable := func() {
		info("Insert migrations table...")
		_, err := dbConn.Exec(context.Background(), "insert into migrations (id) values (1)")
		if err != nil {
			pError(fmt.Sprintf("unable to insert migrations table: %s", err))
		}
	}

	var last sql.NullInt32
	err = dbConn.QueryRow(
		context.Background(),
		"select last from migrations",
	).Scan(&last)

	switch {
	case errors.Is(err, pgx.ErrNoRows):
		insertMigrationsTable()
	case err != nil && err.Error() == "ERROR: relation \"migrations\" does not exist (SQLSTATE 42P01)":
		info("Create migrations table...")
		if _, err := dbConn.Exec(
			context.Background(),
			createMigrationsTable,
		); err != nil {
			pError(fmt.Sprintf("unable to create migrations table: %s", err))
		}

		insertMigrationsTable()
	case err != nil && !errors.Is(err, pgx.ErrNoRows):
		pError(fmt.Sprintf("unable to check if migrations table exists: %s", err))
	}

	dbConn.Close(context.Background())

	info("Executing migrations...")
	toExecute := filterMigrationsToExecute(direction, migrationsFiles, last.Valid, int(last.Int32))

	if len(toExecute) == 0 {
		success("No migrations to execute. Done!")
		return
	}

	var nextLast sql.NullInt32
	nextLast.Valid = true

	for i := 0; i < count && i < len(toExecute); i++ {
		num := toExecute[i]
		filename := fmt.Sprintf("%s_%d.sql", direction, num)
		info(fmt.Sprintf("Executing migration %s", filename))

		filename = filepath.Join(migrationsDirectory, filename)

		cmd := exec.Command("psql", "-U", "user", "-d", "game", "-p", "5432", "-h", "localhost", "-f", filename)
		cmd.Env = append(os.Environ(), "PGPASSWORD=pwd")
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			pError(fmt.Sprintf("unable to run migration: %s", err))
		}

		nextLast.Int32 = int32(num)
	}

	if direction == Down && count >= len(toExecute) {
		nextLast.Valid = false
	}

	info("Updating migrations table...")
	dbConn, err = pgx.Connect(context.Background(), DB_CONN_STRING)
	if err != nil {
		pError(fmt.Sprintf("unable to connect to database: %s", err))
	}

	if _, err = dbConn.Exec(
		context.Background(),
		"update migrations set last = $1 where id = 1",
		nextLast,
	); err != nil {
		pError(fmt.Sprintf("unable to update migrations table: %s", err))
	}

	success("Migration complete!")
}
