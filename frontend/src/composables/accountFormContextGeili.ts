import type { InjectionKey } from 'vue'
import type { adminAPI } from '@/api/admin'
export const accountAdminAPIKeyGeili: InjectionKey<typeof adminAPI> = Symbol('account-admin-api-geili')
export interface AccountInvitationFormGeili { platform: string; name: string; concurrency: number; priority?: number; rate_multiplier?: number; batch_api_keys?: boolean }
export const accountInvitationFormKeyGeili: InjectionKey<AccountInvitationFormGeili> = Symbol('account-invitation-form-geili')
