// geili: administrator-issued commercial invoices.
export default {
  invoice: {
    nav: 'Invoices',
    title: 'Invoices',
    description: 'Issue a commercial invoice for an amount, then print or save it as PDF',
    printTitle: 'Print Invoice',
    issue: 'New Invoice',
    issuerSettings: 'Issuer Details',
    issuerNotReady: 'Issuer details are not set. Fill in the company name and payment details for each currency under "Issuer Details" first.',
    columns: {
      invoiceNo: 'Invoice No.',
      issueDate: 'Issue Date',
      billTo: 'Bill To',
      amount: 'Amount',
      status: 'Status',
      createdAt: 'Created',
      actions: 'Actions'
    },
    status: {
      issued: 'Issued',
      voided: 'Void'
    },
    print: 'Print',
    void: 'Void',
    voidConfirm: 'Void invoice {no}? The number stays reserved and is never reused; printouts show a VOID mark.',
    voided: 'Invoice voided',
    emptyTitle: 'No invoices yet',
    emptyDescription: 'Click "New Invoice" and enter an amount to issue the first one',
    failedToLoad: 'Failed to load invoices',
    form: {
      currency: 'Currency',
      amount: 'Amount',
      amountHint: 'Greater than 0, up to two decimals',
      issueDate: 'Issue date',
      description: 'Description',
      defaultDescription: 'AI API Services',
      billToName: 'Customer name (optional)',
      billToAddress: 'Customer address (optional)',
      notes: 'Notes (optional)',
      paymentPreview: 'Payment details printed on the invoice',
      paymentMissing: 'No payment details for this currency yet. Add them under "Issuer Details" first.',
      submit: 'Issue and print',
      created: 'Issued invoice {no}'
    },
    issuer: {
      companyName: 'Company name',
      companyAddress: 'Company address',
      companyContact: 'Contact',
      companyContactHint: 'Email, phone or website; multiple lines allowed',
      numberPrefix: 'Invoice number prefix',
      numberPrefixHint: 'Uppercase letters or digits, up to 12. Numbers look like {example} and restart at 0001 each year.',
      payment: '{currency} payment details',
      paymentHint: 'Printed as-is, e.g. Wise account holder, account number, SWIFT/BIC, bank name and address',
      footerNote: 'Footer note (optional)',
      seal: 'Company seal',
      sealHint: 'A transparent PNG works best; it is scaled to at most 800 px on upload',
      sealUpload: 'Upload seal',
      sealRemove: 'Remove seal',
      sealNone: 'No seal uploaded',
      sealTooLarge: 'The image is still over 1 MB after processing; choose a smaller one',
      sealInvalid: 'Could not read this image',
      saved: 'Issuer details saved',
      failedToSave: 'Failed to save'
    },
    errors: {
      notReady: 'Company name or payment details for this currency are missing; complete the issuer details first',
      invalid: 'Invalid invoice; check the amount, currency and date',
      issuerInvalid: 'Invalid issuer details; check the company name, prefix and seal image'
    },
    sheet: {
      back: 'Back',
      print: 'Print / Save as PDF',
      printHint: 'In the print dialog choose "Save as PDF" and turn off "Headers and footers".'
    }
  }
}
