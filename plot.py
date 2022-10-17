#!/usr/bin/env python3

import gzip
import json
import sys
import os
import matplotlib.pyplot as plt
import datetime
import numpy as np

from dateutil import parser

def get_json_by_mid(folder, mid):
    res = []
    for file in os.listdir(folder):
        with gzip.open(os.path.join(folder, file), 'r') as fin:
            data = json.loads(fin.read())
            if data.get("MeasurementID") == mid:
                res.append(data)
    return res

def main():
    if len(sys.argv) < 3:
       print("Usage: {} <input folder> <measurement id>".format(sys.argv[0]))
       sys.exit(1)
    
    root_dir = sys.argv[1]
    mid = sys.argv[2]
    json_data = get_json_by_mid(root_dir, mid)

    start_times = [parser.parse(file.get("StartTime")) for file in json_data]
    global_start_time = min(start_times)

    plot_data = {}
    for idx, file in enumerate(json_data):
        x_values, y_values = [], []
        start_time = parser.parse(file.get("StartTime"))
        start_time_offset = start_time - global_start_time
        print("flow #{} time offset: {} us".format(idx, start_time_offset.microseconds))
        measurements = file.get("ServerMeasurements")
        previous = None
        for m in measurements:
            elapsed_sec = m.get("TCPInfo").get("ElapsedTime") / 1000000
            rate_mbps = m.get("TCPInfo").get("BytesAcked") / elapsed_sec / 1000000 * 8
            time = elapsed_sec + start_time_offset.microseconds / 1000000
            if previous is not None:
                dtime = m.get("TCPInfo").get("ElapsedTime") - previous.get("TCPInfo").get("ElapsedTime")
                dbytes = m.get("TCPInfo").get("BytesAcked") - previous.get("TCPInfo").get("BytesAcked")
                x_values.append(time)
                y_values.append(dbytes / dtime * 8)
                print("flow #{} time: {} bytesacked: {}".format(idx, time, m.get("TCPInfo").get("BytesAcked")))
            previous = m

        print("flow #{} rates: {} {}".format(idx, x_values, y_values))
        plot_data[idx] = (x_values, y_values)

    all_x = []
    for k in plot_data:
        x, y = plot_data[k]
        plt.plot(x, y, marker='o', label="flow #{}".format(k))
        all_x = np.unique(np.concatenate((all_x, x)))

    all_y = []
    for k in plot_data:
        x, y = plot_data[k]
        yi = np.interp(all_x, x, y, left=0, right=0)
        all_y.append(yi)
    
    plt.plot(all_x, sum(all_y), marker='o', label="aggr. tp")
    plt.legend()
    plt.ylim(bottom=0)
    plt.show()
    

if __name__ == "__main__":
    main()