import js from '@eslint/js';
import tseslint from 'typescript-eslint';

export default tseslint.config(
  { ignores: ['dist', 'dist-cjs'] },
  js.configs.recommended,
  ...tseslint.configs.recommended,
);
