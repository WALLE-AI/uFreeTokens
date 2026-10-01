// tools/docsPlugin.ts 提供的虚拟模块的类型声明。
declare module 'virtual:docs-manifest' {
  import type { ManifestPage } from '@/src/docs/manifest';
  const pages: ManifestPage[];
  export default pages;
}

declare module 'virtual:docs-search/*' {
  import type { SearchDoc } from '@/src/docs/components/SearchDialog';
  const docs: SearchDoc[];
  export default docs;
}

declare module 'virtual:docs-openapi' {
  import type { OpenAPISpec } from '@/src/docs/api/openapi';
  const spec: OpenAPISpec;
  export default spec;
}
