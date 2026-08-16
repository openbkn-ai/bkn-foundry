import numpy as np
import requests


def bge_score(s1, s2, emb_url):
    embedding_response = requests.post(emb_url, json={
        "texts": [s1, s2]
    }, timeout=300.0)
    if embedding_response.status_code == 200:
        s1_emb = eval(embedding_response.text)[0]
        s2_emb = eval(embedding_response.text)[1]
    else:
        raise Exception

    np_embedding1 = np.array(s1_emb)
    np_embedding2 = np.array(s2_emb)

    cos_score = np_embedding1.dot(np_embedding2) / (np.linalg.norm(np_embedding1) * np.linalg.norm(np_embedding2))
    # print('gbe_sim_score={}'.format(cos_score))
    return cos_score


def evaluate(inputs, props, resource, data_source_config):
    print('start cos_sim metric...')
    answers = inputs['answer']
    ideals = inputs['ideal']
    emb_url = props['emb_url']
    total_error = 0
    total_count = len(answers)
    total_cos_score = 0
    for i in range(len(answers)):
        answer = answers[i]
        ideal = ideals[i]

        print('idx = {}'.format(i))
        print('answer = {}'.format(answer))
        print('ideal = {}'.format(ideal))

        if isinstance(answer, str):
            cos_score = bge_score(answer, ideal, emb_url)
            total_cos_score += cos_score
        elif isinstance(answer, dict):
            if not answer.get('unknown'):
                cos_score = bge_score(answer['text'], ideal, emb_url)
                total_cos_score += cos_score
            else:
                cos_score = 0
                total_error += 1
        else:
            cos_score = 0
            total_error += 1
        print('cos_score = {}'.format(cos_score))
    if total_count - total_error == 0:
        precision = 0.
    else:
        precision = total_cos_score / (total_count - total_error)
    if total_count == 0:
        recall = 0.
    else:
        recall = total_cos_score / total_count
    if precision + recall == 0:
        f1_score = 0.
    else:
        f1_score = 2 * precision * recall / (precision + recall)
    results = {
        'precision': round(precision, 3),
        'recall': round(recall, 3),
        'f1_score': round(f1_score, 3)
    }
    print('results = {}'.format(results))
    return results, ''


class cos_simExecutor:
    cls_type = "Executor"  # Do not change.
    metric_name = "cos_sim"  # Set the metric name here.
    INPUT_TYPE = {
        "ideal": list,
        "answer": list
    }  # Input schema.
    OUTPUT_TYPE = {
        "precision": float,
        "recall": float,
        "f1_score": float
    }  # Output schema.
    metric_input_list = [
        {
            "name": "answer",
            "description": "Algorithm result.",
            "type": "algorithms"
        },
        {
            "name": "ideal",
            "description": "Expected answer.",
            "type": "dataset"
        }
    ]  # Example template input keys and descriptions; type identifies whether the source is data or an algorithm.
    metric_output_list = [
        {
            "name": "precision",
            "description": "Precision: total similarity score divided by predicted answer count."
        },
        {
            "name": "recall",
            "description": "Recall: total similarity score divided by expected answer count."
        },
        {
            "name": "f1_score",
            "description": "F1 is the harmonic mean of precision and recall."
        }
    ]  # Example template output keys and descriptions.
    metric_description = """Encode text with the BGE model and calculate cosine similarity."""
    DEFAULT_PROPS = {
        'emb_url': 'Config.EMB_URL'
    }
    INIT_FUNC = None
    BEFORE_DESTROY = None
    RUN_FUNC = evaluate
