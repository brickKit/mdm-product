package main

import (
	"github.com/brickKit/be-sdk-go/migrate"
	"github.com/brickKit/mdm-product/v2/migrations"
)

func main() { migrate.Main(migrations.FS) }
