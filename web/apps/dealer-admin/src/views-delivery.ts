// The Delivery surface's screen: truck types, delivery windows and delivery zones.
// Pure template function matching the Change 11 Delivery board.
import { html, type TemplateResult } from "lit";

export interface TruckTypeRow {
  readonly type: string;
  readonly capability: string;
  readonly payload: string;
  readonly trucks: string;
}

export interface DeliveryWindowRow {
  readonly name: string;
  readonly hours: string;
}

export interface DeliveryZoneRow {
  readonly name: string;
  readonly coverage: string;
  readonly fee: string;
}

export const TRUCK_TYPES: readonly TruckTypeRow[] = [
  {
    type: "Flatbed with Moffett",
    capability: "Moffett forklift offload, 3 pallets",
    payload: "6.0 t, 5.2 t net after the Moffett",
    trucks: "T01, T04",
  },
  {
    type: "Knuckleboom",
    capability: "Boom offload, bulk bags and pallets",
    payload: "7.0 t net",
    trucks: "T02",
  },
  {
    type: "Dump or tipper",
    capability: "Tips bulk aggregate and soil",
    payload: "10.0 t, 10 yd",
    trucks: "T03",
  },
  {
    type: "Hand unload",
    capability: "Driver and helper, no equipment",
    payload: "1.5 t",
    trucks: "Any truck",
  },
];

export const DELIVERY_WINDOWS: readonly DeliveryWindowRow[] = [
  { name: "Morning", hours: "7:00 AM to 11:00 AM" },
  { name: "Midday", hours: "11:00 AM to 3:00 PM" },
  { name: "Afternoon", hours: "3:00 PM to 5:30 PM" },
];

export const DELIVERY_ZONES: readonly DeliveryZoneRow[] = [
  { name: "Coast", coverage: "Point Loma, Ocean Beach, Midway, La Jolla", fee: "No charge over $500.00" },
  { name: "North County", coverage: "Escondido, Oceanside, Carlsbad", fee: "$75.00" },
  { name: "South Bay", coverage: "Chula Vista, National City, Imperial Beach", fee: "$45.00" },
  { name: "Inland", coverage: "El Cajon, Santee, Lakeside", fee: "$60.00" },
  { name: "Julian Mountain", coverage: "Julian, Ramona, Santa Ysabel", fee: "$140.00, by the ton above 5.0 t" },
];

export function renderDelivery(): TemplateResult {
  return html`
    <header class="page-head">
      <h1>Delivery</h1>
      <p class="page-sub">The trucks, the windows and the zones this dealer delivers with.</p>
    </header>

    <section class="card" data-test="delivery-trucks-card">
      <div class="card-head">
        <h2 class="card-title">Truck types</h2>
      </div>
      <div class="tablewrap">
        <table>
          <thead>
            <tr>
              <th scope="col">TYPE</th>
              <th scope="col">CAPABILITY</th>
              <th scope="col">PAYLOAD</th>
              <th scope="col">TRUCKS</th>
            </tr>
          </thead>
          <tbody>
            ${TRUCK_TYPES.map(
              (truck) => html`
                <tr>
                  <td><strong>${truck.type}</strong></td>
                  <td>${truck.capability}</td>
                  <td>${truck.payload}</td>
                  <td>${truck.trucks}</td>
                </tr>
              `,
            )}
          </tbody>
        </table>
      </div>
    </section>

    <section class="card" data-test="delivery-windows-card">
      <div class="card-head">
        <h2 class="card-title">Delivery windows</h2>
      </div>
      <div class="list-stack">
        ${DELIVERY_WINDOWS.map(
          (win) => html`
            <div class="pill-row">
              <span class="pill-name"><strong>${win.name}</strong></span>
              <span class="pill-detail">${win.hours}</span>
            </div>
          `,
        )}
      </div>
    </section>

    <section class="card" data-test="delivery-zones-card">
      <div class="card-head">
        <h2 class="card-title">Zones</h2>
      </div>
      <div class="list-stack">
        ${DELIVERY_ZONES.map(
          (zone) => html`
            <div class="pill-row">
              <span class="pill-name"><strong>${zone.name}</strong></span>
              <span class="pill-coverage">${zone.coverage}</span>
              <span class="pill-fee">${zone.fee}</span>
            </div>
          `,
        )}
      </div>
    </section>
  `;
}
