// Command applysql applies a .sql file to the test database.
//
// It exists to stage a fault in the live schema so a control can be watched
// going red, which is the only way to tell a control from a comment. It is not
// part of the module's build (see the ignore tag) and nothing in the repository
// depends on it.
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/jackc/pgx/v5"
)

func main() {
	body, err := os.ReadFile(os.Args[1])
	if err != nil {
		panic(err)
	}
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, os.Getenv("SP_REDCHECK_DATABASE_URL"))
	if err != nil {
		panic(err)
	}
	defer func() { _ = conn.Close(ctx) }()
	if _, err := conn.Exec(ctx, string(body)); err != nil {
		panic(err)
	}
	fmt.Println("applied", os.Args[1])
}
