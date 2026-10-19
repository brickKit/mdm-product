package product

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

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

// createCategory creates a category; the same code with the same content again returns it.
func (m *Module) createCategory(c *httpx.Context) error {
	ctx, actor, err := check(c, OpCategory)
	if err != nil {
		return err
	}
	var in categoryIn
	if err := c.BindJSON(&in); err != nil {
		return err
	}
	switch {
	case !codeForm.MatchString(in.Code):
		return invalid("code", "code is upper case letters, digits, _, . and -, at most 48")
	case in.Name == nil || strings.TrimSpace(*in.Name) == "":
		return invalid("name", "name is required")
	case in.ParentID != nil && !uuidForm.MatchString(*in.ParentID):
		return invalid("parent_id", "parent_id is a category's ID, or null")
	}
	names := map[string]string{}
	if in.Names != nil {
		if err := checkNames(*in.Names); err != nil {
			return err
		}
		names = *in.Names
	}
	var out Category
	err = m.rt.DB.Tx(ctx, func(ctx context.Context, tx db.Tx) error {
		var existing string
		if err := tx.QueryRow(ctx, `SELECT id::text FROM category WHERE code = $1 FOR UPDATE`, in.Code).Scan(&existing); err == nil {
			cur, err := getCategory(ctx, tx, existing, false)
			if err != nil {
				return err
			}
			if cur.Name == *in.Name && same(cur.ParentID, in.ParentID) {
				out = cur
				return nil
			}
			return errCode.New("category code " + in.Code + " is taken")
		}
		if in.ParentID != nil {
			p, err := getCategory(ctx, tx, *in.ParentID, false)
			if errors.Is(err, errNone) || err == nil && !p.Active {
				return invalid("parent_id", "no active category "+*in.ParentID)
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
		if out, err = getCategory(ctx, tx, id, false); err != nil {
			return err
		}
		if err := m.emit(ctx, tx, CategoryChanged, out.ID, out.Version, out); err != nil {
			return err
		}
		return m.change(ctx, tx, actor, OpCategory, Categories, out.ID, out)
	})
	if err != nil {
		return err
	}
	return c.JSON(200, out)
}

// updateCategory renames a category or changes whether new products may use it.
func (m *Module) updateCategory(c *httpx.Context) error {
	ctx, actor, err := check(c, OpCategory)
	if err != nil {
		return err
	}
	id := c.Param("id")
	if !uuidForm.MatchString(id) {
		return notFound("category")
	}
	var in categoryIn
	if err := c.BindJSON(&in); err != nil {
		return err
	}
	if in.Code != "" || in.ParentID != nil {
		return invalid("code", "code and parent do not change here")
	}
	var out Category
	err = m.rt.DB.Tx(ctx, func(ctx context.Context, tx db.Tx) error {
		cur, err := getCategory(ctx, tx, id, true)
		if err != nil {
			return err
		}
		if in.Version != nil && *in.Version != cur.Version {
			return errVersion.New(fmt.Sprintf("the category is at version %d", cur.Version))
		}
		next := cur
		if in.Name != nil {
			if strings.TrimSpace(*in.Name) == "" {
				return invalid("name", "name cannot be empty")
			}
			next.Name = *in.Name
		}
		if in.Names != nil {
			if err := checkNames(*in.Names); err != nil {
				return err
			}
			next.Names = *in.Names
		}
		if in.Active != nil {
			next.Active = *in.Active
		}
		if same(cur, next) {
			out = cur
			return nil
		}
		nb, _ := json.Marshal(next.Names)
		if _, err := tx.Exec(ctx, `UPDATE category SET name = $2, names = $3, active = $4, version = version + 1,
			updated_at = now() WHERE id = $1`, id, next.Name, nb, next.Active); err != nil {
			return err
		}
		if out, err = getCategory(ctx, tx, id, false); err != nil {
			return err
		}
		if err := m.emit(ctx, tx, CategoryChanged, out.ID, out.Version, out); err != nil {
			return err
		}
		return m.change(ctx, tx, actor, OpCategory, Categories, out.ID, out)
	})
	if errors.Is(err, errNone) {
		return notFound("category")
	}
	if err != nil {
		return err
	}
	return c.JSON(200, out)
}
