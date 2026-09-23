import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';

const bundlePath = process.argv[2];
if (!bundlePath) {
  throw new Error('Usage: node verify-patched-pricing.mjs <patched-main-bundle.js>');
}

const bundle = readFileSync(bundlePath, 'utf8');
const functionMatch = bundle.match(/qZ=(\(\{record:e,selectedGroup:t,groupRatio:r,tokenUnit:n,displayPrice:i,currency:o,quotaDisplayType:s="USD",precision:l=4\}\)=>\{.*?\}),GZ=/s);
assert.ok(functionMatch, 'calculateModelPrice (qZ) was not found exactly where expected');
assert.equal(bundle.match(/group_ratio_override/g)?.length, 2);

const calculateModelPrice = (0, eval)(`(${functionMatch[1]})`);
const displayPrice = (value) => `$${value.toFixed(3)}`;
const baseRecord = {
  model_name: 'luna-test',
  quota_type: 0,
  model_ratio: 1,
  completion_ratio: 2,
  enable_groups: ['normal', 'vip'],
};
const baseArgs = {
  selectedGroup: 'vip',
  groupRatio: { normal: 1.5, vip: 2 },
  tokenUnit: 'M',
  displayPrice,
  currency: 'USD',
};

const selectedGroupPrice = calculateModelPrice({
  ...baseArgs,
  record: { ...baseRecord },
});
assert.equal(selectedGroupPrice.inputPrice, '$4.0000');
assert.equal(selectedGroupPrice.usedGroup, 'vip');
assert.equal(selectedGroupPrice.usedGroupRatio, 2);

const overriddenPrice = calculateModelPrice({
  ...baseArgs,
  record: { ...baseRecord, group_ratio_override: 1 },
});
assert.equal(overriddenPrice.inputPrice, '$2.0000');
assert.equal(overriddenPrice.completionPrice, '$4.0000');
assert.equal(overriddenPrice.usedGroup, 'vip');
assert.equal(overriddenPrice.usedGroupRatio, 1);

const allGroupPrice = calculateModelPrice({
  ...baseArgs,
  selectedGroup: 'all',
  record: { ...baseRecord },
});
assert.equal(allGroupPrice.inputPrice, '$3.0000');
assert.equal(allGroupPrice.usedGroup, 'normal');
assert.equal(allGroupPrice.usedGroupRatio, 1.5);

const allGroupOverride = calculateModelPrice({
  ...baseArgs,
  selectedGroup: 'all',
  record: { ...baseRecord, group_ratio_override: 1 },
});
assert.equal(allGroupOverride.inputPrice, '$2.0000');
assert.equal(allGroupOverride.usedGroup, 'normal');
assert.equal(allGroupOverride.usedGroupRatio, 1);
assert.deepEqual(allGroupOverride, {
  ...allGroupOverride,
  usedGroup: 'normal',
});

const stringOverrideIgnored = calculateModelPrice({
  ...baseArgs,
  record: { ...baseRecord, group_ratio_override: '1' },
});
assert.equal(stringOverrideIgnored.inputPrice, '$4.0000');
assert.equal(stringOverrideIgnored.usedGroupRatio, 2);

const zeroOverride = calculateModelPrice({
  ...baseArgs,
  record: { ...baseRecord, group_ratio_override: 0 },
});
assert.equal(zeroOverride.inputPrice, '$0.0000');
assert.equal(zeroOverride.usedGroupRatio, 0);

const fixedPriceOverride = calculateModelPrice({
  ...baseArgs,
  record: {
    ...baseRecord,
    quota_type: 1,
    model_price: 3,
    group_ratio_override: 1,
  },
});
assert.equal(fixedPriceOverride.price, '$3.000');
assert.equal(fixedPriceOverride.usedGroupRatio, 1);

const dynamicPriceOverride = calculateModelPrice({
  ...baseArgs,
  record: {
    ...baseRecord,
    billing_mode: 'tiered_expr',
    billing_expr: 'p * 1.3',
    group_ratio_override: 1,
  },
});
assert.equal(dynamicPriceOverride.isDynamicPricing, true);
assert.equal(dynamicPriceOverride.usedGroup, 'vip');
assert.equal(dynamicPriceOverride.usedGroupRatio, 1);

console.log(JSON.stringify({
  bundlePath,
  cases: 8,
  result: 'passed',
}));
