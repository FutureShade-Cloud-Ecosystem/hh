// The dealer mark: the public desk speaks for ONE dealer, so the dealer's
// name is the app's own data, carried here and rendered by the page's header
// band. The public quote route answers no org fields (this page is pre-auth:
// the token is the credential and the answer carries nothing about who served
// it), so there is no server source to read; the name on the header is this
// constant, and the demo build names the demo dealer.
export const DEALER_NAME = "Gable Landscape Supply";
