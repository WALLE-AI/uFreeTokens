import React from 'react';
import { Link } from 'react-router';
import { Code2 } from 'lucide-react';
import { Model } from '../types';

// 模型详情页的「接口调用」卡片：按目录 type（+ 能力）给出该模型对应的接口、最小调用
// 示例与文档链接。端点与类型的对应关系见文档「模型与模型 ID → 模型类型与接口」。
interface EndpointInfo {
  path: string;
  doc: string;
  example: (model: string) => string;
}

const curl = (path: string, body: string) =>
  `curl $BASE_URL${path} \\\n  -H "Authorization: Bearer $UFREETOKENS_API_KEY" \\\n  -H "Content-Type: application/json" \\\n  -d '${body}'`;

function endpointFor(model: Model): EndpointInfo | null {
  const vision = model.modalities.includes('image');
  switch (model.modelType) {
    case 'chat':
      return {
        path: '/v1/chat/completions',
        doc: vision ? 'vision' : 'quickstart',
        example: (m) =>
          vision
            ? curl('/v1/chat/completions', `{"model": "${m}", "messages": [{"role": "user", "content": [{"type": "image_url", "image_url": {"url": "https://example.com/cat.jpg"}}, {"type": "text", "text": "图片里有什么？"}]}]}`)
            : curl('/v1/chat/completions', `{"model": "${m}", "messages": [{"role": "user", "content": "你好"}]}`),
      };
    case 'embedding':
      return { path: '/v1/embeddings', doc: 'embeddings', example: (m) => curl('/v1/embeddings', `{"model": "${m}", "input": "你好"}`) };
    case 'rerank':
      return {
        path: '/v1/rerank',
        doc: 'rerank',
        example: (m) => curl('/v1/rerank', `{"model": "${m}", "query": "苹果手机", "documents": ["iPhone 发布会", "苹果富含维生素"]}`),
      };
    case 'image':
      return {
        path: '/v1/images/generations',
        doc: 'images',
        example: (m) => curl('/v1/images/generations', `{"model": "${m}", "prompt": "水彩风格的橘猫", "size": "1024x1024"}`),
      };
    case 'audio':
      if (model.tags.includes('transcription')) {
        return {
          path: '/v1/audio/transcriptions',
          doc: 'audio',
          example: (m) =>
            `curl $BASE_URL/v1/audio/transcriptions \\\n  -H "Authorization: Bearer $UFREETOKENS_API_KEY" \\\n  -F "file=@speech.mp3" \\\n  -F "model=${m}"`,
        };
      }
      return {
        path: '/v1/audio/speech',
        doc: 'audio',
        example: (m) => `${curl('/v1/audio/speech', `{"model": "${m}", "input": "你好，欢迎使用 uFreeTokens。", "voice": "alex"}`)} \\\n  --output speech.mp3`,
      };
    default:
      return null;
  }
}

export const ModelApiSection: React.FC<{ model: Model }> = ({ model }) => {
  const info = endpointFor(model);
  if (!info) return null;
  return (
    <section id="api" className="space-y-3 pt-2 scroll-mt-28">
      <div className="border-b border-gray-200 pb-3">
        <div className="flex items-center space-x-2">
          <Code2 className="w-4 h-4 text-purple-600" />
          <h2 className="text-base font-bold text-gray-900">接口调用</h2>
        </div>
        <p className="text-xs text-gray-500 mt-1">
          该模型通过 <code className="font-mono text-gray-800">POST {info.path}</code> 调用，与 OpenAI API 兼容。
          <Link to={`/docs/zh/${info.doc}`} className="ml-1 text-purple-700 hover:underline">
            查看文档 →
          </Link>
        </p>
      </div>
      <pre className="m-0 overflow-x-auto rounded-lg bg-[#0d1117] text-gray-200 px-4 py-3 text-[12px] leading-relaxed font-mono">
        {info.example(model.id)}
      </pre>
    </section>
  );
};
