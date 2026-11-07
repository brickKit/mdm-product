package categories

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	core "github.com/brickKit/mdm-product/v3/internal/core"

	sdkauthz "github.com/brickKit/be-sdk-go/v3/authz"
	"github.com/brickKit/be-sdk-go/v3/list"

	"github.com/brickKit/be-sdk-go/v3/db"
	"github.com/brickKit/be-sdk-go/v3/httpx"
	"github.com/brickKit/be-sdk-go/v3/ids"
)

type categoryIn struct {
	Version  *int64             `json:"version"`
	Code     string             `json:"code"`
	Name     *string            `json:"name"`
	Names    *map[string]string `json:"names"`
	ParentID *string            `json:"parent_id"`
	Active   *bool              `json:"active"`
}

// CreateCategory creates a category; the same code with the same content again returns it.
func CreateCategory(m *core.Base, c *httpx.Context) error {
	ctx, actor, err := core.Check(c, core.OpCategory)
	if err != nil {
		return err
	}
	var in categoryIn
	if err := c.BindJSON(&in); err != nil {
		return err
	}
	switch {
	case !core.CodeForm.MatchString(in.Code):
		return core.Invalid("code", "code is upper case letters, digits, _, . and -, at most 48")
	case in.Name == nil || strings.TrimSpace(*in.Name) == "":
		return core.Invalid("name", "name is required")
	case in.ParentID != nil && !core.UUIDForm.MatchString(*in.ParentID):
		return core.Invalid("parent_id", "parent_id is a category's ID, or null")
	}
	names := map[string]string{}
	if in.Names != nil {
		if err := core.CheckNames(*in.Names); err != nil {
			return err
		}
		names = *in.Names
	}
	var out core.Category
	err = m.RT.DB.Tx(ctx, func(ctx context.Context, tx db.Tx) error {
		var existing string
		if err := tx.QueryRow(ctx, `SELECT id::text FROM category WHERE code = $1 FOR UPDATE`, in.Code).Scan(&existing); err == nil {
			cur, err := core.GetCategory(ctx, tx, existing, false)
			if err != nil {
				return err
			}
			if cur.Name == *in.Name && core.Same(cur.ParentID, in.ParentID) {
				out = cur
				return nil
			}
			return core.ErrCode.New("category code " + in.Code + " is taken")
		}
		if in.ParentID != nil {
			p, err := core.GetCategory(ctx, tx, *in.ParentID, false)
			if errors.Is(err, core.ErrNone) || err == nil && !p.Active {
				return core.Invalid("parent_id", "no active category "+*in.ParentID)
			}
			if err != nil {
				return err
			}
		}
		id := ids.New().String()
		nb, _ := json.Marshal(names)
		if _, err := tx.Exec(ctx, `INSERT INTO category (id, code, name, names, parent_id) VALUES ($1, $2, $3, $4, $5)`,
			id, in.Code, *in.Name, nb, in.ParentID); err != nil {
			return err
		}
		var err error
		if out, err = core.GetCategory(ctx, tx, id, false); err != nil {
			return err
		}
		if err := core.Emit(m, ctx, tx, core.CategoryChanged, out.ID, out.Version, out); err != nil {
			return err
		}
		return core.Change(m, ctx, tx, actor, core.OpCategory, core.Categories, out.ID, out)
	})
	if err != nil {
		return err
	}
	return c.JSON(200, out)
}

// UpdateCategory renames a category or changes whether new products may use it.
func UpdateCategory(m *core.Base, c *httpx.Context) error {
	ctx, actor, err := core.Check(c, core.OpCategory)
	if err != nil {
		return err
	}
	id := c.Param("id")
	if !core.UUIDForm.MatchString(id) {
		return core.NotFound("category")
	}
	var in categoryIn
	if err := c.BindJSON(&in); err != nil {
		return err
	}
	if in.Code != "" || in.ParentID != nil {
		return core.Invalid("code", "code and parent do not change here")
	}
	var out core.Category
	err = m.RT.DB.Tx(ctx, func(ctx context.Context, tx db.Tx) error {
		cur, err := core.GetCategory(ctx, tx, id, true)
		if err != nil {
			return err
		}
		if in.Version != nil && *in.Version != cur.Version {
			return core.ErrVersion.New(fmt.Sprintf("the category is at version %d", cur.Version))
		}
		next := cur
		if in.Name != nil {
			if strings.TrimSpace(*in.Name) == "" {
				return core.Invalid("name", "name cannot be empty")
			}
			next.Name = *in.Name
		}
		if in.Names != nil {
			if err := core.CheckNames(*in.Names); err != nil {
				return err
			}
			next.Names = *in.Names
		}
		if in.Active != nil {
			next.Active = *in.Active
		}
		if core.Same(cur, next) {
			out = cur
			return nil
		}
		nb, _ := json.Marshal(next.Names)
		if _, err := tx.Exec(ctx, `UPDATE category SET name = $2, names = $3, active = $4, version = version + 1,
			updated_at = now() WHERE id = $1`, id, next.Name, nb, next.Active); err != nil {
			return err
		}
		if out, err = core.GetCategory(ctx, tx, id, false); err != nil {
			return err
		}
		if err := core.Emit(m, ctx, tx, core.CategoryChanged, out.ID, out.Version, out); err != nil {
			return err
		}
		return core.Change(m, ctx, tx, actor, core.OpCategory, core.Categories, out.ID, out)
	})
	if errors.Is(err, core.ErrNone) {
		return core.NotFound("category")
	}
	if err != nil {
		return err
	}
	return c.JSON(200, out)
}

var categoryList = list.Spec{Operation: core.OpRead, Alias: "c", IDColumn: "id", IDKind: list.UUID,
	Columns: []list.Column{{Field: "code", Column: "code", Kind: list.Text, Sortable: true}}}

func ListCategories(m *core.Base, c *httpx.Context) error {
	limit, err := list.ParseLimit(c.Query("limit"))
	if err != nil {
		return err
	}
	s, _ := sdkauthz.From(c.Ctx())
	ctx, q, err := list.Build(c.Ctx(), s, categoryList, list.Request{Limit: limit, Cursor: c.Query("cursor"),
		Sort: []list.Order{{Field: "code"}}})
	if err != nil {
		return err
	}
	out, next, err := list.Page(ctx, m.RT.DB, q, "category", core.CategoryCols, core.ScanCategory,
		func(v core.Category) ([]string, string) { return []string{v.Code}, v.ID })
	if err != nil {
		return err
	}
	return c.JSON(200, map[string]any{"categories": out, "next_cursor": next})
}
