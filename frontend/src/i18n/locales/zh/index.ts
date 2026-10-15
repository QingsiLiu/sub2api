import landing from './landing'
import common from './common'
import dashboard from './dashboard'
import channelMonitorV2 from './channelMonitorV2'
import batchImage from './batchImage'
import admin from './admin'
import misc from './misc'

import subscriptionRights from './subscriptionRights'

import responseAudit from './responseAudit'
import supplier from './supplier'

export default {
  ...responseAudit,
  ...supplier,
  ...subscriptionRights,
  ...landing,
  ...common,
  ...dashboard,
  ...channelMonitorV2,
  ...batchImage,
  admin,
  ...misc,
}
