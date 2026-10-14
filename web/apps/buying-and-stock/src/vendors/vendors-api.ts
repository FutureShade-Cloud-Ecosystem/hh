// The vendors area's one door to the core: the vendor records, the ordering contact, the vendor's
// own codes and the echoed order reference, over the generated client, answering a Result and never
// throwing. Every route here is one the core serves.
import {
  staffProduct,
  staffPurchasing,
  type ApiClient,
  type ProductPage,
  type PurchasingCreateVendorRequest,
  type PurchasingPO,
  type PurchasingUpdateVendorRequest,
  type PurchasingVendorContactResult,
  type PurchasingVendorContactWrite,
  type PurchasingVendorMaster,
  type PurchasingVendorPage,
  type PurchasingVendorSKU,
  type PurchasingVendorSKUPage,
  type PurchasingVendorSKUWrite,
  type Result,
} from "@hardscapehouse/api-client";

export class VendorsApi {
  readonly #client: ApiClient;

  constructor(client: ApiClient) {
    this.#client = client;
  }

  list(): Promise<Result<PurchasingVendorPage>> {
    return staffPurchasing.purchasingListVendors(this.#client);
  }

  create(body: PurchasingCreateVendorRequest): Promise<Result<PurchasingVendorMaster>> {
    return staffPurchasing.purchasingCreateVendor(this.#client, body);
  }

  update(id: string, body: PurchasingUpdateVendorRequest): Promise<Result<PurchasingVendorMaster>> {
    return staffPurchasing.purchasingUpdateVendor(this.#client, id, body);
  }

  contact(id: string): Promise<Result<PurchasingVendorContactResult>> {
    return staffPurchasing.purchasingGetVendorContact(this.#client, id);
  }

  setContact(id: string, body: PurchasingVendorContactWrite): Promise<Result<PurchasingVendorContactResult>> {
    return staffPurchasing.purchasingSetVendorContact(this.#client, id, body);
  }

  removeContact(id: string): Promise<Result<PurchasingVendorContactResult>> {
    return staffPurchasing.purchasingDeleteVendorContact(this.#client, id);
  }

  codes(id: string): Promise<Result<PurchasingVendorSKUPage>> {
    return staffPurchasing.purchasingListVendorSKUs(this.#client, id);
  }

  setCode(id: string, body: PurchasingVendorSKUWrite): Promise<Result<PurchasingVendorSKU>> {
    return staffPurchasing.purchasingSetVendorSKU(this.#client, id, body);
  }

  removeCode(id: string, productId: string): Promise<Result<unknown>> {
    return staffPurchasing.purchasingDeleteVendorSKU(this.#client, id, productId);
  }

  /** The catalogue the code picker lists. */
  products(): Promise<Result<ProductPage>> {
    return staffProduct.productList(this.#client, { active: true, limit: 200 });
  }

  /** The order a vendor quoted back, found by the number as the vendor wrote it. */
  orderByRef(ref: string): Promise<Result<PurchasingPO>> {
    return staffPurchasing.purchasingResolveVendorRef(this.#client, { ref });
  }
}
