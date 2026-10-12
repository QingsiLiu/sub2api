import { inject, hasInjectionContext } from 'vue'
import { adminAPI } from '@/api/admin'
import { accountAdminAPIKeyGeili } from './accountFormContextGeili'
export { accountAdminAPIKeyGeili, accountInvitationFormKeyGeili } from './accountFormContextGeili'
// The official form/OAuth flows resolve one API inside their setup lifecycle.
export function useAccountAdminAPIGeili() {
  return hasInjectionContext() ? inject(accountAdminAPIKeyGeili, adminAPI) : adminAPI
}
