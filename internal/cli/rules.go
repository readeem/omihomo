package cli

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/peterbourgon/ff/v3/ffcli"
	"github.com/readeem/omihomo/internal/client"
	"github.com/readeem/omihomo/internal/protocol"
)

// Rule indices count leaves in document order, including disabled rules.
func ruleAt(entries *[]protocol.RuleEntry, index int) (*[]protocol.RuleEntry, int, string, error) {
	n := 0
	for i := range *entries {
		e := &(*entries)[i]
		if e.Category == nil {
			n++
			if n == index {
				return entries, i, "", nil
			}
			continue
		}
		for j := range e.Category.Rules {
			n++
			if n == index {
				return &e.Category.Rules, j, e.Category.ID, nil
			}
		}
	}
	return nil, 0, "", &exitError{ExitNotFound, fmt.Errorf("there is no rule %d of yours", index)}
}

func categoryAt(entries []protocol.RuleEntry, id string) (int, error) {
	for i, e := range entries {
		if e.Category != nil && e.Category.ID == id {
			return i, nil
		}
	}
	return 0, &exitError{ExitNotFound, fmt.Errorf("there is no editable category %q", id)}
}

func positive(text string) (int, error) {
	n, err := strconv.Atoi(text)
	if err != nil || n < 1 {
		return 0, fmt.Errorf("indices are positive numbers")
	}
	return n, nil
}

func assignRule(v *protocol.ApplyArgs, r protocol.RuleEntry, index int, category string) error {
	if category != "" {
		if _, err := categoryAt(v.Rules, category); err != nil {
			return err
		}
	}
	if index > 0 {
		list, at, old, err := ruleAt(&v.Rules, index)
		if err != nil {
			return err
		}
		if old == category {
			(*list)[at] = r
			return nil
		}
		*list = slices.Delete(*list, at, at+1)
	}
	if category == "" {
		v.Rules = append(v.Rules, r)
	} else {
		at, _ := categoryAt(v.Rules, category)
		v.Rules[at].Category.Rules = append(v.Rules[at].Category.Rules, r)
	}
	return nil
}

func (a *app) ruleCommand() *ffcli.Command {
	edit := func(name, synopsis, help string, min, max, indices int, run func(context.Context, *client.Client, *protocol.ApplyArgs, []string) error) *ffcli.Command {
		return a.edit(name, synopsis, help, protocol.OpRules, func(cmd *ffcli.Command, args []string) (change, error) {
			if len(args) < min || len(args) > max {
				return nil, usage(cmd, "see usage for the required arguments")
			}
			for _, arg := range args[:indices] {
				if _, err := positive(arg); err != nil {
					return nil, usage(cmd, "%s", err)
				}
			}
			return func(ctx context.Context, c *client.Client, v *protocol.ApplyArgs) error { return run(ctx, c, v, args) }, nil
		})
	}
	save := func(update bool) func(context.Context, *client.Client, *protocol.ApplyArgs, []string) error {
		return func(ctx context.Context, c *client.Client, v *protocol.ApplyArgs, args []string) error {
			index := 0
			category := ""
			disabled := false
			if update {
				var err error
				index, err = positive(args[0])
				if err != nil {
					return err
				}
				args = args[1:]
				list, at, old, err := ruleAt(&v.Rules, index)
				if err != nil {
					return err
				}
				category = old
				disabled = (*list)[at].Disabled
			}
			r := protocol.RuleEntry{Type: strings.ToUpper(strings.TrimSpace(args[0])), Value: args[1], Target: args[2], Disabled: disabled}
			if len(args) > 3 {
				r.Params = args[3]
			}
			if len(args) > 4 {
				category = args[4]
			}
			if len(args) > 5 {
				enabled, err := strconv.ParseBool(args[5])
				if err != nil {
					return fmt.Errorf("enabled must be true or false")
				}
				r.Disabled = !enabled
			}
			if r.Type != "SUB-RULE" {
				err := checkName(ctx, c, r.Target, false)
				if err != nil {
					if index == 0 {
						return err
					}
					list, at, _, findErr := ruleAt(&v.Rules, index)
					if findErr != nil || (*list)[at].Target != r.Target {
						return err
					}
				}
			}
			return assignRule(v, r, index, category)
		}
	}
	toggleRule := func(enabled bool) func(context.Context, *client.Client, *protocol.ApplyArgs, []string) error {
		return func(_ context.Context, _ *client.Client, v *protocol.ApplyArgs, args []string) error {
			n, err := positive(args[0])
			if err != nil {
				return err
			}
			list, at, _, err := ruleAt(&v.Rules, n)
			if err != nil {
				return err
			}
			(*list)[at].Disabled = !enabled
			return nil
		}
	}
	categoryToggle := func(enabled bool) func(context.Context, *client.Client, *protocol.ApplyArgs, []string) error {
		return func(_ context.Context, _ *client.Client, v *protocol.ApplyArgs, args []string) error {
			switch args[0] {
			case "omihomo":
				v.Settings.OmihomoRulesDisabled = !enabled
			case "subscription":
				v.Settings.SubscriptionRulesDisabled = !enabled
			default:
				at, err := categoryAt(v.Rules, args[0])
				if err != nil {
					return err
				}
				v.Rules[at].Disabled = !enabled
			}
			return nil
		}
	}
	moveEntry := func(_ context.Context, _ *client.Client, v *protocol.ApplyArgs, args []string) error {
		from, err := positive(args[0])
		if err != nil {
			return err
		}
		to, err := positive(args[1])
		if err != nil {
			return err
		}
		if from > len(v.Rules) || to > len(v.Rules) {
			return fmt.Errorf("entry indices are outside your rules")
		}
		e := v.Rules[from-1]
		v.Rules = slices.Delete(v.Rules, from-1, from)
		v.Rules = slices.Insert(v.Rules, to-1, e)
		return nil
	}
	return a.group("rule", "omihomo rule <command>", "manage ordered rules and named categories",
		a.call("list", "omihomo rule list [--pretty]", "list rules and categories in match order", nargs(0, protocol.OpRules, nil)),
		edit("add", "omihomo rule add <type> <value> <target> [params] [category-id] [enabled]", "add a rule", 3, 6, 0, save(false)),
		edit("update", "omihomo rule update <index> <type> <value> <target> [params] [category-id] [enabled]", "edit or move a rule between categories", 4, 7, 1, save(true)),
		edit("enable", "omihomo rule enable <index>", "enable a rule", 1, 1, 1, toggleRule(true)),
		edit("disable", "omihomo rule disable <index>", "disable a rule", 1, 1, 1, toggleRule(false)),
		edit("move", "omihomo rule move <from> <to>", "reorder rules within their category", 2, 2, 2, func(_ context.Context, _ *client.Client, v *protocol.ApplyArgs, args []string) error {
			from, err := positive(args[0])
			if err != nil {
				return err
			}
			to, err := positive(args[1])
			if err != nil {
				return err
			}
			list, at, category, err := ruleAt(&v.Rules, from)
			if err != nil {
				return err
			}
			_, dest, other, err := ruleAt(&v.Rules, to)
			if err != nil {
				return err
			}
			if category != other {
				return fmt.Errorf("choose a rule in the same category; use update to change category")
			}
			r := (*list)[at]
			*list = slices.Delete(*list, at, at+1)
			*list = slices.Insert(*list, dest, r)
			return nil
		}),
		edit("entry-move", "omihomo rule entry-move <from> <to>", "reorder a category or standalone rule", 2, 2, 2, moveEntry),
		edit("remove", "omihomo rule remove <index>", "remove a rule", 1, 1, 1, func(_ context.Context, _ *client.Client, v *protocol.ApplyArgs, args []string) error {
			n, err := positive(args[0])
			if err != nil {
				return err
			}
			list, at, _, err := ruleAt(&v.Rules, n)
			if err != nil {
				return err
			}
			*list = slices.Delete(*list, at, at+1)
			return nil
		}),
		a.group("category", "omihomo rule category <command>", "manage named rule categories",
			edit("add", "omihomo rule category add <name>", "create an empty category", 1, 1, 0, func(_ context.Context, _ *client.Client, v *protocol.ApplyArgs, args []string) error {
				var id [16]byte
				if _, err := rand.Read(id[:]); err != nil {
					return err
				}
				v.Rules = append(v.Rules, protocol.RuleEntry{Category: &protocol.RuleCategory{ID: hex.EncodeToString(id[:]), Name: args[0], Rules: []protocol.RuleEntry{}}})
				return nil
			}),
			edit("rename", "omihomo rule category rename <id> <name>", "rename a category", 2, 2, 0, func(_ context.Context, _ *client.Client, v *protocol.ApplyArgs, args []string) error {
				at, err := categoryAt(v.Rules, args[0])
				if err != nil {
					return err
				}
				v.Rules[at].Category.Name = args[1]
				return nil
			}),
			edit("enable", "omihomo rule category enable <id>", "enable a category", 1, 1, 0, categoryToggle(true)),
			edit("disable", "omihomo rule category disable <id>", "disable a category", 1, 1, 0, categoryToggle(false)),
			edit("target", "omihomo rule category target <id> <target>", "change all routing targets in a user category", 2, 2, 0, func(ctx context.Context, c *client.Client, v *protocol.ApplyArgs, args []string) error {
				at, err := categoryAt(v.Rules, args[0])
				if err != nil {
					return err
				}
				if err := checkName(ctx, c, args[1], false); err != nil {
					return err
				}
				for i := range v.Rules[at].Category.Rules {
					r := &v.Rules[at].Category.Rules[i]
					if r.Type != "SUB-RULE" {
						r.Target = args[1]
					}
				}
				return nil
			}),
			edit("remove", "omihomo rule category remove <id>", "ungroup a category without deleting its rules", 1, 1, 0, func(_ context.Context, _ *client.Client, v *protocol.ApplyArgs, args []string) error {
				at, err := categoryAt(v.Rules, args[0])
				if err != nil {
					return err
				}
				e := v.Rules[at]
				rules := e.Category.Rules
				if e.Disabled {
					for i := range rules {
						rules[i].Disabled = true
					}
				}
				v.Rules = slices.Replace(v.Rules, at, at+1, rules...)
				return nil
			}),
		),
	)
}
