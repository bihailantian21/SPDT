from flask import Flask, request, jsonify
import json
from processing import getMeassures, plotGraph

app = Flask(__name__)

@app.route("/api/peaks", methods=['POST'])
def processSignal():
    threshold = request.json['threshold']
    serie = request.json['serie']
    response, peaks, valleys, properties, propValleys, vector, invvector = getMeassures(serie, threshold)
    y ={"PoI": response}
    return jsonify(y)

@app.route("/api/peaks/plot", methods=['POST'])
def processAndPlotSignal():
    threshold = request.json['threshold']
    serie = request.json['serie']
    response,peaks, valleys, properties, propValleys, vector, invvector = getMeassures(serie, threshold)
    plotGraph(serie, peaks, valleys, properties, propValleys, vector, invvector, threshold, response)
    y ={"PoI": response}
    return jsonify(y)


if __name__ == "__main__":
    app.run(port=5003)