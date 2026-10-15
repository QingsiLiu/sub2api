// geili: supplier console and supplier-owner administration.
export default {
  supplier: {
    roleName: 'Supplier',
    navSection: 'Supplier Console',
    nav: {
      accounts: 'Accounts',
      usage: 'Usage Records'
    },
    accounts: {
      title: 'Accounts',
      description: 'Manage the upstream accounts you uploaded',
      add: 'Upload Account',
      edit: 'Edit Account',
      searchPlaceholder: 'Search account name',
      allPlatforms: 'All platforms',
      allStates: 'All states',
      reviewNotice: 'New accounts are enabled only after an administrator reviews them and assigns groups. Changing the API key or base URL pauses scheduling until an administrator confirms again.',
      columns: {
        name: 'Name',
        platform: 'Platform',
        state: 'State',
        concurrency: 'Concurrency',
        groups: 'Groups',
        rateMultiplier: 'Settlement rate',
        lastUsed: 'Last used',
        createdAt: 'Uploaded',
        actions: 'Actions'
      },
      states: {
        pending: 'Pending review',
        running: 'Running',
        paused: 'Paused',
        disabled: 'Disabled',
        error: 'Error'
      },
      noGroups: 'Ungrouped',
      form: {
        platform: 'Platform',
        name: 'Name',
        notes: 'Notes',
        apiKey: 'API Key',
        apiKeyEditHint: 'Current key: {hint}. Leave empty to keep it',
        baseUrl: 'Base URL',
        baseUrlHint: 'Leave empty for the official endpoint; public addresses only',
        concurrency: 'Concurrency',
        concurrencyHint: '1-1000'
      },
      pause: 'Pause',
      pauseConfirm: 'Only an administrator can resume scheduling. Pause "{name}"?',
      delete: 'Delete',
      deleteConfirm: 'Delete account "{name}"? This cannot be undone; usage history is kept.',
      test: 'Test',
      testTitle: 'Test account: {name}',
      testModel: 'Model',
      testModelPlaceholder: 'Leave empty for the default model',
      startTest: 'Start test',
      testing: 'Testing…',
      testSuccess: 'Test passed',
      testFailed: 'Test failed',
      created: 'Account uploaded and waiting for review',
      updated: 'Account updated',
      pausedDone: 'Scheduling paused',
      deleted: 'Account deleted',
      failedToLoad: 'Failed to load accounts',
      failedToSave: 'Failed to save',
      emptyTitle: 'No accounts yet',
      emptyDescription: 'Upload your first API key account'
    },
    usage: {
      title: 'Usage Records',
      description: 'Requests served by the accounts you uploaded',
      allAccounts: 'All accounts',
      modelPlaceholder: 'Filter by model',
      costHint: 'Account cost = standard cost × account settlement rate, for reference only. Customer details are not shown.',
      stats: {
        requests: 'Requests',
        tokens: 'Total tokens',
        accountCost: 'Account cost',
        avgDuration: 'Avg duration'
      },
      columns: {
        time: 'Time',
        account: 'Account',
        model: 'Model',
        input: 'Input',
        output: 'Output',
        cache: 'Cache read/write',
        accountCost: 'Account cost',
        duration: 'Duration'
      },
      stream: 'Stream',
      failedToLoad: 'Failed to load usage records'
    },
    admin: {
      owner: 'Supplier',
      viewAccounts: 'Supplier accounts',
      setOwner: 'Set supplier',
      setOwnerTitle: 'Set supplier of "{name}"',
      ownerUserId: 'Supplier user ID',
      ownerUserIdHint: 'User ID of a user with the Supplier role; 0 clears the owner',
      ownerSaved: 'Supplier updated',
      ownerFilter: 'Showing accounts of supplier #{id} only',
      clearOwnerFilter: 'Clear'
    }
  }
}
