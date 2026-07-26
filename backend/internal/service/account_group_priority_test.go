//go:build unit

package service

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAccountSchedulingPriority_IsScopedToGroup(t *testing.T) {
	account := Account{
		ID:       7,
		Priority: 50,
		AccountGroups: []AccountGroup{
			{AccountID: 7, GroupID: 101, Priority: 5},
			{AccountID: 7, GroupID: 202, Priority: 2},
		},
	}

	account.SetGroupSchedulingPriority(101)
	require.Equal(t, 5, account.SchedulingPriority())
	require.Equal(t, 50, account.Priority)

	account.SetGroupSchedulingPriority(202)
	require.Equal(t, 2, account.SchedulingPriority())
	require.Equal(t, 50, account.Priority)

	account.SetGroupSchedulingPriority(303)
	require.Equal(t, 50, account.SchedulingPriority())
	require.Nil(t, account.GroupPriority)
}

func TestApplyGroupSchedulingPriority_AllowsDifferentValuesForSameAccount(t *testing.T) {
	accounts := []Account{{
		ID:       9,
		Priority: 40,
		AccountGroups: []AccountGroup{
			{AccountID: 9, GroupID: 1, Priority: 8},
			{AccountID: 9, GroupID: 2, Priority: 3},
		},
	}}

	ApplyGroupSchedulingPriority(accounts, 1)
	require.Equal(t, 8, accounts[0].SchedulingPriority())
	require.Equal(t, 40, accounts[0].Priority)

	ApplyGroupSchedulingPriority(accounts, 2)
	require.Equal(t, 3, accounts[0].SchedulingPriority())
	require.Equal(t, 40, accounts[0].Priority)
}

func TestFilterByMinPriority_UsesGroupPriority(t *testing.T) {
	groupPriority := 2
	accounts := []accountWithLoad{
		{
			account:  &Account{ID: 1, Priority: 50, GroupPriority: &groupPriority},
			loadInfo: &AccountLoadInfo{},
		},
		{
			account:  &Account{ID: 2, Priority: 10},
			loadInfo: &AccountLoadInfo{},
		},
	}

	result := filterByMinPriority(accounts)
	require.Len(t, result, 1)
	require.Equal(t, int64(1), result[0].account.ID)
	require.Equal(t, 50, result[0].account.Priority)
	require.Equal(t, 2, result[0].account.SchedulingPriority())
}

func TestAccountGroupPriority_IsNotSerializedAsGlobalState(t *testing.T) {
	priority := 4
	account := Account{
		ID:            11,
		Priority:      30,
		GroupPriority: &priority,
		AccountGroups: []AccountGroup{{AccountID: 11, GroupID: 77, Priority: 4}},
	}

	payload, err := json.Marshal(account)
	require.NoError(t, err)

	var decoded Account
	require.NoError(t, json.Unmarshal(payload, &decoded))
	require.Equal(t, 30, decoded.Priority)
	require.Nil(t, decoded.GroupPriority)
	require.Len(t, decoded.AccountGroups, 1)
	require.Equal(t, int64(77), decoded.AccountGroups[0].GroupID)
	require.Equal(t, 4, decoded.AccountGroups[0].Priority)
}
