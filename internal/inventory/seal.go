// SPDX-License-Identifier: Apache-2.0

package inventory

import (
	"github.com/AxiomOperator/dbr2/internal/secrets"
)

// Sealer encrypts and decrypts secret values (auth.SecretBox).
type Sealer interface {
	Seal(plaintext []byte, ad string) ([]byte, error)
	Open(sealed []byte, ad string) ([]byte, error)
}

// SealAD is the associated data binding sealed inventory values to an agent.
func SealAD(agentID string) string { return "dbr2:inventory:" + agentID }

// Seal classifies and seals every sensitive value before the inventory is
// stored (threat model T9): environment values are removed and kept only in
// sealed form; Compose and .env files keep a masked copy for display and a
// sealed original for authorized reveal and for backups.
func Seal(inv *Inventory, s Sealer, ad string) error {
	for ci := range inv.Containers {
		c := &inv.Containers[ci]
		for ei := range c.Env {
			e := &c.Env[ei]
			e.Sensitive = secrets.IsSensitive(e.Key, e.Value)
			if !e.Sensitive || e.Value == "" {
				continue
			}
			sealed, err := s.Seal([]byte(e.Value), ad)
			if err != nil {
				return err
			}
			e.Sealed, e.Value = sealed, ""
		}
	}
	for pi := range inv.ComposeProjects {
		p := &inv.ComposeProjects[pi]
		for fi := range p.ConfigFiles {
			if err := sealFile(&p.ConfigFiles[fi], secrets.MaskCompose, s, ad); err != nil {
				return err
			}
		}
		for fi := range p.EnvFiles {
			if err := sealFile(&p.EnvFiles[fi], secrets.MaskEnvFile, s, ad); err != nil {
				return err
			}
		}
	}
	return nil
}

func sealFile(f *File, mask func(string) (string, bool), s Sealer, ad string) error {
	if f.Content == "" {
		return nil
	}
	masked, changed := mask(f.Content)
	if !changed {
		return nil
	}
	sealed, err := s.Seal([]byte(f.Content), ad)
	if err != nil {
		return err
	}
	f.Content, f.Masked, f.Sealed = masked, true, sealed
	return nil
}

// Reveal decrypts sealed values in place (callers must hold secrets.read and
// audit the reveal).
func Reveal(inv *Inventory, s Sealer, ad string) error {
	for ci := range inv.Containers {
		for ei := range inv.Containers[ci].Env {
			e := &inv.Containers[ci].Env[ei]
			if len(e.Sealed) == 0 {
				continue
			}
			v, err := s.Open(e.Sealed, ad)
			if err != nil {
				return err
			}
			e.Value = string(v)
		}
	}
	for pi := range inv.ComposeProjects {
		p := &inv.ComposeProjects[pi]
		for _, fs := range [][]File{p.ConfigFiles, p.EnvFiles} {
			for fi := range fs {
				if len(fs[fi].Sealed) == 0 {
					continue
				}
				v, err := s.Open(fs[fi].Sealed, ad)
				if err != nil {
					return err
				}
				fs[fi].Content = string(v)
			}
		}
	}
	return nil
}

// Redact prepares an inventory for API output: sealed material is dropped and
// sensitive environment values are replaced by the mask.
func Redact(inv *Inventory) {
	for ci := range inv.Containers {
		for ei := range inv.Containers[ci].Env {
			e := &inv.Containers[ci].Env[ei]
			if e.Sensitive {
				e.Value = secrets.Mask
			}
			e.Sealed = nil
		}
	}
	for pi := range inv.ComposeProjects {
		p := &inv.ComposeProjects[pi]
		for _, fs := range [][]File{p.ConfigFiles, p.EnvFiles} {
			for fi := range fs {
				fs[fi].Sealed = nil
			}
		}
	}
}
