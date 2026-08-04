import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { describe, expect, it } from "vitest";
import {
  API_CATALOG_SCHEMA_VERSION,
  APICatalogContractError,
  isValidAPIType,
  isValidCustomAPIType,
  parseAPICatalog,
} from "./api-catalog";

interface CatalogFixtureEntry {
  api_type: string;
  label: string;
  description: string;
  display_order: number;
  semantic_error_supported: boolean;
  response_protocol_ids: unknown[];
}

interface CatalogFixture {
  schema_version: number;
  custom_api_type_prefix: string;
  api_types: CatalogFixtureEntry[];
}

function loadCatalogFixture(): CatalogFixture {
  return JSON.parse(
    readFileSync(
      resolve(process.cwd(), "../contracts/internal-error/v1/api-catalog.json"),
      "utf8",
    ),
  ) as CatalogFixture;
}

describe("parseAPICatalog", () => {
  it("decodes every shared v1 field and freezes the trusted projection", () => {
    const wireCatalog = loadCatalogFixture();
    const catalog = parseAPICatalog(wireCatalog);

    expect(catalog).toEqual(wireCatalog);
    expect(catalog.schema_version).toBe(API_CATALOG_SCHEMA_VERSION);
    expect(catalog.api_types.map((entry) => entry.display_order)).toEqual([
      0, 1, 2, 3, 4, 5,
    ]);
    expect(
      catalog.api_types.flatMap((entry) => entry.response_protocol_ids),
    ).toEqual(
      wireCatalog.api_types.flatMap((entry) => entry.response_protocol_ids),
    );
    expect(Object.isFrozen(catalog)).toBe(true);
    expect(Object.isFrozen(catalog.api_types)).toBe(true);
    expect(
      catalog.api_types.every(
        (entry) =>
          Object.isFrozen(entry) &&
          Object.isFrozen(entry.response_protocol_ids),
      ),
    ).toBe(true);
  });

  it("uses display_order rather than transport array order", () => {
    const wireCatalog = loadCatalogFixture();
    wireCatalog.api_types.reverse();

    const catalog = parseAPICatalog(wireCatalog);

    expect(catalog.api_types.map((entry) => entry.display_order)).toEqual([
      0, 1, 2, 3, 4, 5,
    ]);
    expect(catalog.api_types[0].api_type).toBe("claude");
  });

  it.each([
    {
      name: "unknown schema",
      mutate: (catalog: CatalogFixture) => {
        catalog.schema_version = 2;
      },
      path: "schema_version",
    },
    {
      name: "path-bearing custom prefix",
      mutate: (catalog: CatalogFixture) => {
        catalog.custom_api_type_prefix = "custom/";
      },
      path: "custom_api_type_prefix",
    },
    {
      name: "empty built-in list",
      mutate: (catalog: CatalogFixture) => {
        catalog.api_types = [];
      },
      path: "api_types",
    },
    {
      name: "duplicate API type",
      mutate: (catalog: CatalogFixture) => {
        catalog.api_types[1].api_type = catalog.api_types[0].api_type;
      },
      path: "duplicates API type",
    },
    {
      name: "duplicate display order",
      mutate: (catalog: CatalogFixture) => {
        catalog.api_types[1].display_order = catalog.api_types[0].display_order;
      },
      path: "duplicates display order",
    },
  ])("rejects $name", ({ mutate, path }) => {
    const wireCatalog = loadCatalogFixture();
    mutate(wireCatalog);

    expect(() => parseAPICatalog(wireCatalog)).toThrowError(
      expect.objectContaining<Partial<APICatalogContractError>>({
        name: "APICatalogContractError",
        message: expect.stringContaining(path),
      }),
    );
  });

  it.each([
    {
      name: "missing protocol array",
      protocols: undefined,
      semanticSupport: true,
    },
    {
      name: "non-string protocol ID",
      protocols: [42],
      semanticSupport: true,
    },
    {
      name: "empty protocol ID",
      protocols: [""],
      semanticSupport: true,
    },
    {
      name: "duplicate protocol ID",
      protocols: ["protocol.v1", "protocol.v1"],
      semanticSupport: true,
    },
    {
      name: "supported entry without a protocol",
      protocols: [],
      semanticSupport: true,
    },
  ])("rejects $name", ({ protocols, semanticSupport }) => {
    const wireCatalog = loadCatalogFixture();
    wireCatalog.api_types[0].response_protocol_ids = protocols as unknown[];
    wireCatalog.api_types[0].semantic_error_supported = semanticSupport;

    expect(() => parseAPICatalog(wireCatalog)).toThrow(APICatalogContractError);
  });

  it("allows an unsupported built-in to advertise no response protocols", () => {
    const wireCatalog = loadCatalogFixture();
    wireCatalog.api_types[0].semantic_error_supported = false;
    wireCatalog.api_types[0].response_protocol_ids = [];

    expect(
      parseAPICatalog(wireCatalog).api_types[0].response_protocol_ids,
    ).toEqual([]);
  });
});

describe("API type validation", () => {
  it("derives built-in membership from the fetched catalog", () => {
    const wireCatalog = loadCatalogFixture();
    wireCatalog.api_types[0].api_type = "future-messages";
    const catalog = parseAPICatalog(wireCatalog);

    expect(isValidAPIType(catalog, "future-messages")).toBe(true);
    expect(isValidAPIType(catalog, "claude")).toBe(false);
  });

  it.each([
    ["custom:tool", true],
    ["custom:foo.bar", true],
    ["custom:", false],
    ["custom:foo/bar", false],
    ["custom:.", false],
    ["custom:..", false],
    ["other:tool", false],
  ])("validates custom path syntax for %s", (apiType, expected) => {
    const catalog = parseAPICatalog(loadCatalogFixture());

    expect(isValidCustomAPIType(catalog, apiType)).toBe(expected);
    expect(isValidAPIType(catalog, apiType)).toBe(expected);
  });

  it("uses the prefix from the wire contract", () => {
    const wireCatalog = loadCatalogFixture();
    wireCatalog.custom_api_type_prefix = "extension:";
    const catalog = parseAPICatalog(wireCatalog);

    expect(isValidCustomAPIType(catalog, "extension:tool")).toBe(true);
    expect(isValidCustomAPIType(catalog, "custom:tool")).toBe(false);
  });
});
