# No-op DRA Driver

Author: @johnbelamaric

This DRA driver:
- Does not publish any ResourceSlices
- Registers with the kubelet with one or more names
- Returns success for all gRPC calls

This is useful for controller-based DRA drivers that do not need to do anything
on the node. Eventually we hope to make that usage pattern available without
running this driver, but this helps for now.
