#!/usr/bin/env python3
"""Adapt an existing Qwen3 reranker GGUF to llama.cpp's classification format.

Requires gguf==0.19.0 and numpy==2.5.4 in a separate Python environment.
Transformer tensors retain their original quantization. Only the yes/no output
rows are dequantized to FP32, following llama.cpp conversion/qwen.py.
This does not turn an ordinary Qwen chat model into a trained reranker.
"""
import argparse
from pathlib import Path

import gguf
import numpy as np

TEMPLATE = (
    '<|im_start|>system\nJudge whether the Document meets the requirements based on the Query and the Instruct provided. '
    'Note that the answer can only be "yes" or "no".<|im_end|>\n'
    '<|im_start|>user\n<Instruct>: Given a web search query, retrieve relevant passages that answer the query\n'
    '<Query>: {query}\n<Document>: {document}<|im_end|>\n'
    '<|im_start|>assistant\n<think>\n\n</think>\n\n'
)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('source', type=Path)
    parser.add_argument('destination', type=Path)
    args = parser.parse_args()
    if args.destination.exists():
        parser.error('destination already exists; use a new output path')
    reader = gguf.GGUFReader(args.source)
    arch = reader.fields['general.architecture'].contents()
    name = reader.fields['general.name'].contents()
    if arch != 'qwen3' or 'reranker' not in name.lower():
        parser.error('expected a trained Qwen3 Reranker GGUF')
    tensors = {tensor.name: tensor for tensor in reader.tensors}
    if 'cls.output.weight' in tensors:
        parser.error('model already contains a classification head')
    # A missing output.weight means the LM head is tied to token embeddings.
    head = tensors.get('output.weight', tensors['token_embd.weight'])
    tokens = reader.fields['tokenizer.ggml.tokens'].contents()
    rows = [tokens.index(label) for label in ['yes', 'no']]
    classifier = gguf.dequantize(head.data[rows], head.tensor_type).astype(np.float32)
    writer = gguf.GGUFWriter(args.destination, arch)
    for key, field in reader.fields.items():
        if key.startswith('GGUF.') or key == 'general.architecture' or key.startswith('tokenizer.chat_template') or key in ('qwen3.pooling_type', 'qwen3.classifier.output_labels'):
            continue
        writer.add_key_value(key, field.contents(), field.types[0], field.types[-1] if len(field.types) > 1 else None)
    writer.add_pooling_type(gguf.PoolingType.RANK)
    writer.add_classifier_output_labels(['yes', 'no'])
    writer.add_chat_template([{'name': 'rerank', 'template': TEMPLATE}])
    for tensor in reader.tensors:
        if tensor.name != 'output.weight':
            writer.add_tensor(tensor.name, tensor.data, raw_dtype=tensor.tensor_type)
    writer.add_tensor('cls.output.weight', classifier)
    writer.write_header_to_file()
    writer.write_kv_data_to_file()
    writer.write_tensors_to_file()
    writer.close()
    print(f'Created {args.destination} with yes/no classifier rows {rows}')


if __name__ == '__main__':
    main()
