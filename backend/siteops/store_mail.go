package siteops

// storageStore embeds *storage.SiteOpsStore and promotes its mail operations.
var _ MailStore = (*storageStore)(nil)
