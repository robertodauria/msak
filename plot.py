#!/usr/bin/env python3

import gzip
import json
import sys
import os
import matplotlib.pyplot as plt
import matplotlib.ticker as plticker
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
        measurements = file.get("ClientMeasurements")
        previous = None
        for m in measurements:
            elapsed_sec = m.get("AppInfo").get("ElapsedTime") / 1000000
            rate_mbps = m.get("AppInfo").get("NumBytes") / elapsed_sec / 1000000 * 8
            time = elapsed_sec + start_time_offset.microseconds / 1000000
            if previous is not None:
                dtime = m.get("AppInfo").get("ElapsedTime") - previous.get("AppInfo").get("ElapsedTime")
                dbytes = m.get("AppInfo").get("NumBytes") - previous.get("AppInfo").get("NumBytes")
                x_values.append(time)
                y_values.append(dbytes / dtime * 8)
            previous = m
        plot_data[idx] = (x_values, y_values)
        plt.plot(x_values, y_values, marker='x', label="Flow ID: ...{}".format(file.get("UUID")[-5:]))


    all_x = []
    for k in plot_data:
        x, y = plot_data[k]
        all_x = np.unique(np.concatenate((all_x, x)))

    all_y = []
    for k in plot_data:
        x, y = plot_data[k]
        yi = np.interp(all_x, x, y, left=0, right=0)
        all_y.append(yi)
    
    plt.title("MeasurementID: {}".format(mid))
    plt.xlabel("Time (s)")
    plt.ylabel("Throughput (Mb/s)")
    plt.plot(all_x, sum(all_y),label="aggregate")
    plt.legend(loc='upper right')
    plt.ylim(bottom=0)
    
    ax = plt.subplot(111)
    loc = plticker.MultipleLocator(base=1)
    ax.xaxis.set_major_locator(loc)
    
    plt.show()
    
if __name__ == "__main__":
    main()