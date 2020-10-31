package auth

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
)

type ghOrg struct {
	Login string `json:"login"`
}

func GithubOrg(org string) func(string, string) error {
	return func(_, ghToken string) error {
		req, err := http.NewRequest("GET", "https://api.github.com/user/orgs", nil)
		if err != nil {
			return err
		}
		req.Header.Set("Authorization", "Bearer "+ghToken)

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return err
		}
		defer resp.Body.Close()

		var ghOrgs []ghOrg
		if err := json.NewDecoder(resp.Body).Decode(&ghOrgs); err != nil {
			return err
		}

		for _, ghOrg := range ghOrgs {
			if ghOrg.Login == org {
				return nil
			}
		}
		return fmt.Errorf("user must be member of Github organization %s", org)
	}
}

func Static(u, p string) func(string, string) error {
	return func(username, password string) error {
		if u != username || p != password {
			return errors.New("invalid credentials")
		}
		return nil
	}
}
