#!/usr/bin/env python3

import json
import sys
import os
import matplotlib

import pylab

def main():
    if len(sys.argv) < 2:
        print("Usage: {} <input folder 1> <input folder 2>".format(sys.argv[0]))
        sys.exit(1)

    fig, axes = pylab.subplots(3, 2, sharey="all")
    # Set horizontal spacing
    fig.subplots_adjust(hspace=0.5)

    # Read all folders in each folder passed as argument
    for col, root_dir in enumerate(sys.argv[1:]):
        nstream_dirs = os.listdir(root_dir)
        nstream_dirs.sort()
        # Create a subplot for each folder. 
        for row, nstreams_dir in enumerate(nstream_dirs):
            files = os.listdir(os.path.join(root_dir, nstreams_dir))
            files.sort()
            for i, file in enumerate(files):
                if file.startswith("aggregate"):
                    with open(os.path.join(root_dir, nstreams_dir, file)) as f:
                        data = json.load(f)
                        times = [float(x) for x in data.keys()]
                        axes[row, col].plot(times, data.values(), label="aggregate")
                else: 
                    with open(os.path.join(root_dir, nstreams_dir, file)) as f:
                        data = json.load(f)
                        # Get the x,y values from the data
                        avg_x, avg_y = [], []
                        rate_x, rate_y = [], []
                        previous = None
                        for measurement in data.get("Download").get("ServerMeasurements"):
                            time = measurement.get("TCPInfo").get("ElapsedTime") / 1000000
                            rate = measurement.get("TCPInfo").get("BytesAcked") / time / 1000000 * 8
                            avg_x.append(time)
                            avg_y.append(rate)
                            if previous is not None:
                                dtime = measurement.get("TCPInfo").get("ElapsedTime") - previous.get("TCPInfo").get("ElapsedTime")
                                dbytes = measurement.get("TCPInfo").get("BytesAcked") - previous.get("TCPInfo").get("BytesAcked")
                                rate_x.append(time)
                                rate_y.append(dbytes / dtime * 8)
                            previous = measurement

                        axes[row][col].plot(avg_x, avg_y, "x:", label="avg. rate {}".format(i))
                        axes[row][col].plot(rate_x, rate_y, "x-", label="rate {}".format(i))

            # Make sure the ticks are always visible.
            axes[row][col].tick_params(labelleft=True)
            # Set custom step.
            axes[row][col].yaxis.set_major_locator(matplotlib.ticker.MultipleLocator(base=50.0))
            # Set title and labels.
            axes[row][col].set_title("{} - {} streams".format(root_dir, nstreams_dir))
            axes[row][col].set_xlabel("Time (s)")
            axes[row][col].set_ylabel("Throughput (Mbps)")
            # Set legend location.
            axes[row][col].legend(loc="upper left")
            # Enable grid.
            axes[row][col].grid()
                    

    pylab.show()

if __name__ == "__main__":
    main()